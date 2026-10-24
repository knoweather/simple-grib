package grib

import (
	"encoding/binary"
	"fmt"
	"io"
	"iter"
	"os"

	"github.com/knoweather/simple-grib/grib1"
)

// msgRef is one entry in the file's message index. A classic single-field
// message yields exactly one ref with field == -1. A multi-field envelope
// (JMA GPV style: sections 1 and 3 shared, [s4..s7] repeated) yields one ref
// per data-field group; all refs of the same envelope share off/length/env.
//
// GRIB1 messages are always classic (field == -1, env == nil): the format has
// no multi-field envelope concept, one message is one product.
type msgRef struct {
	off     int64          // 第 0 节第 1 字节的偏移（信封起点）/ byte offset of section 0 octet 1 (envelope start)
	length  int            // 总长度（GRIB2：第 9-16 字节；GRIB1：第 0 节第 5-7 字节）/ total length (GRIB2: octets 9-16; GRIB1: octets 5-7 of section 0)
	edition int            // 1 或 2；决定选用哪个解析器 / 1 or 2; selects the parser
	field   int            // -1 = 经典消息；k = 信封的第 k 个数据字段组 / -1 = classic message; k = k-th data-field group of an envelope
	env     *envelopeIndex // 同信封各 ref 共享的多字段布局；经典消息为 nil / multi-field layout shared by this envelope's refs; nil for classic
}

// File 表示一个已打开的 GRIB 文件（版本 1 或 2，可能混合），其消息索引在
// 打开时建立。索引只读取第 0 节头部，因此打开多消息大文件开销很小；消息
// 正文按需延迟读取。
//
// File 支持混合文件：每条消息按其自身版本解析，返回的 Message 用
// Message.Edition() 区分。数据服务常把 GRIB1 与 GRIB2 产品放在同一目录。
//
// File is an open GRIB file (edition 1 or 2, possibly mixed) whose message
// index was built at open time. The index reads only the section 0 headers,
// so opening a large multi-message file is cheap; message bodies are read
// lazily on demand.
//
// File supports mixed files: each message is parsed by its own edition and
// the returned Messages are told apart with Message.Edition(). Data services
// routinely keep GRIB1 and GRIB2 products in the same directory.
type File struct {
	name   string
	r      io.ReaderAt
	size   int64
	closef func() error
	msgs   []msgRef
	cfg    fileConfig // 解析消息时应用的已解析 Option 值 / resolved Option values, applied when parsing messages
}

// Open 打开 path 并扫描其 GRIB 消息索引。返回的 File 必须关闭（通常用 defer）。
//
// Open opens path and scans its GRIB message index. The returned File must
// be closed (typically with defer).
func Open(path string, opts ...Option) (*File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, err
	}
	file, err := openReader(fh, st.Size(), opts...)
	if err != nil {
		fh.Close()
		return nil, err
	}
	file.name = path
	file.closef = fh.Close
	return file, nil
}

// OpenReader 包装一个已知大小的现有 io.ReaderAt（例如内存缓冲区）。
// 所有权仍归调用方；这类文件的 File.Close 是空操作。
//
// OpenReader wraps an existing io.ReaderAt of known size (e.g. an in-memory
// buffer). Ownership stays with the caller; File.Close is a no-op for such files.
func OpenReader(r io.ReaderAt, size int64, opts ...Option) (*File, error) {
	return openReader(r, size, opts...)
}

func openReader(r io.ReaderAt, size int64, opts ...Option) (*File, error) {
	cfg := &fileConfig{}
	for _, o := range opts {
		if o == nil {
			continue
		}
		o(cfg)
	}
	f := &File{r: r, size: size, cfg: *cfg}
	if size < 8 {
		return nil, fmt.Errorf("%w (%d bytes)", ErrTruncated, size)
	}
	var off int64
	for off < size {
		// 第 0 节最小长度：GRIB1 为 8 字节，GRIB2 为 16 字节。先读前
		// 8 字节取得版本号，再按版本读余下部分——两种头部布局完全不同。
		//
		// Minimum section 0 length: 8 bytes for GRIB1, 16 for GRIB2. Read the
		// first 8 bytes to get the edition, then read the rest per edition —
		// the two header layouts are entirely different.
		if size-off < 8 {
			return nil, fmt.Errorf("%w at offset %d (%d trailing bytes)", ErrTruncated, off, size-off)
		}
		var hdr [16]byte
		if _, err := r.ReadAt(hdr[:8], off); err != nil {
			return nil, fmt.Errorf("grib: read section 0 header at offset %d: %w", off, err)
		}
		if string(hdr[0:4]) != "GRIB" {
			return nil, fmt.Errorf("%w at offset %d (magic % X)", ErrNotGRIB2, off, hdr[0:4])
		}

		edition := int(hdr[7])
		var totalLen int64
		switch edition {
		case 1:
			// GRIB1 第 0 节（IS）只有 8 字节：'GRIB' + 3 字节总长度 +
			// 版本号。24 位长度使单条 GRIB1 消息上限为 16 MB——这是
			// FM 92-IX 固有的限制。
			//
			// GRIB1 section 0 (IS) is only 8 bytes: 'GRIB' + 3-octet total
			// length + edition. The 24-bit length caps a single GRIB1 message
			// at 16 MB — a limit inherent to FM 92-IX.
			totalLen = int64(uint32(hdr[4])<<16 | uint32(hdr[5])<<8 | uint32(hdr[6]))
			if totalLen < 8 {
				return nil, fmt.Errorf("%w at offset %d (GRIB1 total length %d)", ErrBadSectionLength, off, totalLen)
			}
			if off+totalLen > size {
				return nil, fmt.Errorf("%w at offset %d (message needs %d bytes, only %d left)",
					ErrTruncated, off, totalLen, size-off)
			}
			// GRIB1 没有多字段信封：一条消息就是一个记录。
			// GRIB1 has no multi-field envelope: one message is one record.
			f.msgs = append(f.msgs, msgRef{off: off, length: int(totalLen), edition: 1, field: -1})
			off += totalLen
			continue
		case 2:
			// GRIB2：落入下方 16 字节的第 0 节处理路径。
			// GRIB2: fall through to the 16-byte section 0 path below.
		default:
			return nil, fmt.Errorf("%w at offset %d (edition %d)", ErrUnsupportedEdition, off, edition)
		}

		if size-off < 16 {
			return nil, fmt.Errorf("%w at offset %d (%d trailing bytes)", ErrTruncated, off, size-off)
		}
		if _, err := r.ReadAt(hdr[:], off); err != nil {
			return nil, fmt.Errorf("grib: read section 0 header at offset %d: %w", off, err)
		}
		// Message total length: octets 9-16, an 8-octet field (WMO GRIB2
		// section 0; ecCodes section.0.def "section_length[8] totalLength").
		// For messages < 2^32 a producer may place the value in either half;
		// reading all 8 octets is correct for any compliant encoding (the
		// other half is zero).
		totalLen = int64(binary.BigEndian.Uint64(hdr[8:16]))
		if totalLen < 16 {
			return nil, fmt.Errorf("%w at offset %d (total length %d)", ErrBadSectionLength, off, totalLen)
		}
		if off+totalLen > size {
			return nil, fmt.Errorf("%w at offset %d (message needs %d bytes, only %d left)",
				ErrTruncated, off, totalLen, size-off)
		}
		// Classify the envelope's section layout. A classic single-field
		// message (section numbers strictly increasing) keeps the legacy
		// whole-body path; a multi-field envelope expands to one logical
		// message per data-field group.
		ei, err := walkEnvelope(r, off, totalLen)
		if err != nil {
			return nil, fmt.Errorf("%w (envelope at offset %d)", err, off)
		}
		if ei == nil {
			f.msgs = append(f.msgs, msgRef{off: off, length: int(totalLen), edition: 2, field: -1})
		} else {
			for k := range ei.groups {
				f.msgs = append(f.msgs, msgRef{off: off, length: int(totalLen), edition: 2, field: k, env: ei})
			}
		}
		off += totalLen
	}
	return f, nil
}

// Close releases the underlying file handle. Calling it twice is safe,
// and it does nothing for files opened via OpenReader.
func (f *File) Close() error {
	if f.closef == nil {
		return nil
	}
	err := f.closef()
	f.closef = nil
	return err
}

// NumMessages returns the number of logical messages in the file (known at
// open time). It never fails for an open File.
func (f *File) NumMessages() (int, error) {
	return len(f.msgs), nil
}

// Message returns message i (0-based), reading its full body from the
// underlying reader and validating the section structure. For a multi-field
// envelope, index i selects one data-field group: the returned message is the
// virtual view [s0][s1][s3][that group's sections] (assembled contiguously),
// and its Size() reports the envelope's declared totalLength.
func (f *File) Message(i int) (*Message, error) {
	if i < 0 || i >= len(f.msgs) {
		return nil, fmt.Errorf("grib: message index %d out of range [0,%d)", i, len(f.msgs))
	}
	ref := f.msgs[i]
	buf := make([]byte, ref.length)
	if _, err := f.r.ReadAt(buf, ref.off); err != nil {
		return nil, fmt.Errorf("grib: read message %d at offset %d: %w", i, ref.off, err)
	}
	// Edition dispatch: GRIB1 goes to the grib1 package, which is wrapped
	// into the same facade; GRIB2 keeps the regular path below. The dispatch
	// lives here rather than earlier so both editions share the byte reading.
	if ref.edition == 1 {
		g1, err := grib1.Parse(i, ref.off, buf)
		if err != nil {
			return nil, err
		}
		return newGRIB1Message(i, ref.off, buf, g1), nil
	}
	if ref.field < 0 {
		return newMessageOpts(i, ref.off, buf, len(buf), f.cfg.parseOpts())
	}
	// Multi-field: assemble the single-field view from the envelope bytes.
	ei := ref.env
	view := make([]byte, 0, 16+32)
	view = append(view, buf[0:16]...) // section 0 (shared header)
	for _, s := range ei.shared {
		s0 := int(s.Off - ref.off)
		view = append(view, buf[s0:s0+s.Length]...)
	}
	for _, s := range ei.groups[ref.field] {
		s0 := int(s.Off - ref.off)
		view = append(view, buf[s0:s0+s.Length]...)
	}
	return newMessageOpts(i, ref.off, view, ref.length, f.cfg.parseOpts())
}

// Messages iterates all messages in file order. An error terminates the
// iteration; when a yielded message is non-nil the error is necessarily nil.
func (f *File) Messages() iter.Seq2[*Message, error] {
	n := len(f.msgs)
	return func(yield func(*Message, error) bool) {
		for i := 0; i < n; i++ {
			m, err := f.Message(i)
			if !yield(m, err) {
				return
			}
		}
	}
}
