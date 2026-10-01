package scenario

func init() {
	register(Definition{
		Name:        "server_error_then_success",
		Description: "The first create calls of a request get a 5xx and create nothing, then one succeeds.",
		Params: []Param{
			{Name: "failures", Type: "int", Default: "1", Description: "How many calls fail before one succeeds (1 to 10)."},
			{Name: "status", Type: "int", Default: "503", Allowed: []string{"503", "500", "502", "504"}, Description: "HTTP status of the failed calls."},
		},
		build: func(v *Values) Scenario {
			return serverErrorThenSuccess{
				base:     base{name: "server_error_then_success"},
				failures: v.Int("failures", 1, 10),
				status:   v.Int("status", 500, 599),
			}
		},
	})
}

type serverErrorThenSuccess struct {
	base
	failures int
	status   int
}

func (s serverErrorThenSuccess) Refuse(attempt int) (int, bool) {
	return s.status, attempt < s.failures
}
