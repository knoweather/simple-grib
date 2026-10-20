package grib

import "time"

// StepRange returns the message's forecast interval as two absolute times,
// mirroring what ecCodes reports as the forecastTime / step / validityDate
// triple:
//
//	begin = reference time + forecastTime   (interval start; ecCodes "forecastTime")
//	end   = interval end                    (ecCodes "step" = end − reference time, "validityDate")
//
// For instantaneous messages begin == end. For WMO statistical templates
// (GRIB2 4.8-4.14) the interval is delimited by the template's "time of end
// of overall time interval" timestamp — the table 4.4 forecast-time field
// only marks the *start*, so reading it alone under-reports the step by a
// whole interval (6h accumulations would read step=0, validity 6h early).
// For GRIB1 the interval is decoded from the P1/P2 pair under WMO table 5
// semantics by the grib1 package.
//
// ok is false when the product has no forecast-time field at all, when the
// time unit has no fixed duration (months, years — callers have to interpret
// Unit/Raw themselves, same rule as Product.ForecastTime), or on a parse
// error. It is never false merely because the interval end is absent: that
// only happens for instantaneous products, which report begin == end.
//
// oks false vs "no end known": a GRIB2 message whose end-of-interval
// timestamp is parametrized (templates 4.34/4.114/…, see
// spec.EndIntervalParametrized) reports begin == end — its interval length
// cannot be recovered from section 4 alone.
func (m *Message) StepRange() (begin, end time.Time, ok bool) {
	id, err := m.Identification()
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	p, err := m.Product()
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if m.g1 != nil {
		b, e, ok := m.g1.Product().StepRange()
		if !ok {
			return time.Time{}, time.Time{}, false
		}
		return id.ReferenceTime.Add(b), id.ReferenceTime.Add(e), true
	}
	if p.ForecastTime == nil || p.ForecastTime.Unit > 2 {
		// No time field in this template, or a unit without fixed length
		// (months/years): no interval can be stated.
		return time.Time{}, time.Time{}, false
	}
	begin = id.ReferenceTime.Add(p.ForecastTime.Value)
	end = begin
	if p.EndOfOverallInterval != nil {
		end = *p.EndOfOverallInterval
	}
	return begin, end, true
}

// Step returns the forecast step — end − reference time, the duration ecCodes
// calls "step". For an instantaneous product StepRange returns begin == end
// and this is 0. ok=false has the same meaning as in StepRange.
func (m *Message) Step() (time.Duration, bool) {
	id, err := m.Identification()
	if err != nil {
		return 0, false
	}
	_, end, ok := m.StepRange()
	if !ok {
		return 0, false
	}
	return end.Sub(id.ReferenceTime), true
}
