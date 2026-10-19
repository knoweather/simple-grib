package spec

import (
	"fmt"
	"strconv"
)

// LocalBandLowMin 等四个常量划定“本地使用”模板号区间。中心区间 192-254 是
// WMO 为本地使用预留、由各带模板的 section 共享的范围；高位区间 32768-65534
// 覆盖两字节模板号字段的上半段。注册还要求 (section, number) 不与已有 WMO 定义
// 冲突 —— 因此“按 section 的实际占用”由冲突检查强制（例如 section 4，WMO 已在
// 192-253 分配了模板）。
//
// LocalBandLowMin and its three companion constants delimit the local-use
// template number bands. The central band 192-254 is reserved by WMO for local
// use and shared by all template-bearing sections; the high band 32768-65534
// covers the upper half of the two-byte template number field. Registration
// also requires the (section, number) pair not to collide with an existing WMO
// definition — so per-section occupancy is enforced by the conflict check (e.g.
// in section 4 WMO already allocated templates in 192-253).
const (
	LocalBandLowMin  = 192
	LocalBandLowMax  = 254
	LocalBandHighMin = 32768
	LocalBandHighMax = 65534
)

// localTemplates 记录通过 RegisterLocalTable 注册的 key，使同一对的重复注册
// 能替换此前本地定义，而 WMO 定义保持不可变。
//
// localTemplates records the keys registered through RegisterLocalTable, so
// re-registering the same pair replaces the previous local definition while
// WMO definitions stay immutable.
var localTemplates = map[string]bool{}

// RegisterLocalTable 为某个“本地使用”模板号注册调用方提供的自定义定义，
// 使 Template() 的查找、基于 Walk 的解析与名称显示能作用于使用中心本地
// 模板的消息。
//
// section 必须是带模板的 section（1、3、4、5、6 或 7 —— section 0 是固定布局，
// section 2 是原始本地使用数据）；number 必须落在本地区间（192-254 或 32768-65534），
// 且不能与已有 WMO 定义冲突。重复注册同一本地对会替换之前的定义。
//
// 注册时先校验再深拷贝：注册表只保存校验通过的副本，不保留（也不修改）
// 调用方的 *TemplateDef，因此并发注册互不干扰，调用方后续对结构体的修改
// 也不会影响注册表。Start/Count 不构成连贯固定范围的字段在此被拒绝，
// 而不是拖到 NewWalk 才报错。
//
// RegisterLocalTable registers a caller-supplied custom definition for a
// local-use template number, so that Template() lookups, Walk-based parsing,
// and name display cover messages that use central local templates.
//
// section must be a template-bearing section (1, 3, 4, 5, 6 or 7: section 0
// has a fixed layout, section 2 is raw local-use data); number must fall in a
// local band (192-254 or 32768-65534) and must not collide with an existing
// WMO definition. Re-registering the same local pair replaces the previous
// definition.
//
// Registration validates before deep-copying: the registry stores only the
// validated copy and neither retains nor mutates the caller's *TemplateDef,
// so concurrent registrations do not interfere and later caller mutations of
// the struct cannot reach the registry. Fields whose Start/Count do not form a
// coherent fixed range are rejected here instead of being deferred to NewWalk.
func RegisterLocalTable(section, number int, def *TemplateDef) error {
	if section == 0 || section == 2 || section < 1 || section > 7 {
		return fmt.Errorf("spec: RegisterLocalTable: section %d has no template structure (template-bearing sections are 1,3,4,5,6,7)", section)
	}
	inLow := number >= LocalBandLowMin && number <= LocalBandLowMax
	inHigh := number >= LocalBandHighMin && number <= LocalBandHighMax
	if !inLow && !inHigh {
		return fmt.Errorf("spec: RegisterLocalTable: template %d.%d outside local bands (%d-%d or %d-%d)",
			section, number, LocalBandLowMin, LocalBandLowMax, LocalBandHighMin, LocalBandHighMax)
	}
	if def == nil {
		return fmt.Errorf("spec: RegisterLocalTable: nil definition for template %d.%d", section, number)
	}
	key := strconv.Itoa(section) + "." + strconv.Itoa(number)

	// 校验 + 深拷贝。固定位置字段需要连贯的 [Start, Start+Count) 范围；
	// Variable/Symbolic 字段没有固定位置，Start 被忽略。
	// Validate and deep-copy: fixed-position fields need a coherent
	// [Start, Start+Count) range; Variable/Symbolic fields have no fixed
	// position, so their Start is ignored.
	fields := make([]Field, len(def.Fields))
	copy(fields, def.Fields)
	for i, f := range fields {
		switch f.Kind {
		case KindVariable, KindSymbolic:
			// 无固定字节；Start 被忽略
			// No fixed bytes; Start is ignored
		case KindU8, KindU16, KindU32, KindF32:
			if f.Start < 0 {
				return fmt.Errorf("spec: RegisterLocalTable: template %s field %d: negative Start %d", key, i, f.Start)
			}
			want := kindWidth(f.Kind)
			if f.Count != want {
				return fmt.Errorf("spec: RegisterLocalTable: template %s field %d: kind %d needs Count %d, got %d",
					key, i, f.Kind, want, f.Count)
			}
		default:
			return fmt.Errorf("spec: RegisterLocalTable: template %s field %d: unknown kind %d", key, i, f.Kind)
		}
	}
	clone := &TemplateDef{ID: def.ID, Name: def.Name, Status: def.Status, Fields: fields}
	if clone.ID == "" {
		clone.ID = key
	}

	mu.Lock()
	defer mu.Unlock()
	if _, isWMO := templatesByKey[key]; isWMO && !localTemplates[key] {
		return fmt.Errorf("spec: RegisterLocalTable: template %s already has a WMO definition; local registration cannot override it", key)
	}
	templatesByKey[key] = clone
	localTemplates[key] = true
	return nil
}

// kindWidth 返回普通整型/浮点字段种类的固定字节宽度。
//
// kindWidth returns the fixed byte width of a plain integer or
// floating-point field kind.
func kindWidth(k Kind) int {
	switch k {
	case KindU8:
		return 1
	case KindU16:
		return 2
	case KindU32, KindF32:
		return 4
	default:
		return 0
	}
}
