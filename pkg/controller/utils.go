package controller

func safe(s *string) string {
	if s == nil {
		return "(null)"
	}
	return *s
}
