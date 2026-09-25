package scenario

func init() {
	register(Definition{
		Name:        "happy_path",
		Description: "pending -> captured (or authorized with manual capture), one callback per event.",
		build:       func(*Values) Scenario { return base{name: "happy_path"} },
	})
}
