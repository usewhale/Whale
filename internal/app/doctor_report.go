package app

func (r DoctorReport) Summary() (ok, warn, fail int) {
	for _, c := range r.Checks {
		switch c.Level {
		case DoctorOK:
			ok++
		case DoctorWarn:
			warn++
		case DoctorFail:
			fail++
		}
	}
	return ok, warn, fail
}

func (r DoctorReport) HasFailures() bool {
	_, _, fail := r.Summary()
	return fail > 0
}
