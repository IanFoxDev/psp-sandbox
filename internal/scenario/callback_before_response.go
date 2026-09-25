package scenario

import "time"

func init() {
	register(Definition{
		Name: "callback_before_response",
		Description: "The status changes and the callback is sent before the create call answers. " +
			"The answer comes lead later and still says pending.",
		Params: []Param{
			{Name: "lead", Type: "duration", Default: "50ms", Description: "Time between the first callback attempt and the create response."},
		},
		build: func(v *Values) Scenario {
			return callbackBeforeResponse{base: base{name: "callback_before_response"}, lead: v.Duration("lead")}
		},
	})
}

type callbackBeforeResponse struct {
	base
	lead time.Duration
}

func (s callbackBeforeResponse) OnCreate(c CreateContext) Plan {
	step := settle(c)
	step.After = 0
	return Plan{
		Response: Response{AfterFirstCallback: true, Delay: s.lead},
		Steps:    []Step{step},
	}
}
