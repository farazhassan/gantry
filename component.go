package gantry

// Component is anything that can wire itself into an agent. Component packages
// (e.g. components/memory) provide constructors that return a Component; install
// them with Agent.With or the WithComponents option.
type Component interface {
	Install(*Agent) error
}

// With installs components into an already-constructed agent, in order,
// stopping and returning at the first install error. Nil components are skipped.
func (a *Agent) With(cs ...Component) error {
	for _, c := range cs {
		if c == nil {
			continue
		}
		if err := c.Install(a); err != nil {
			return err
		}
	}
	return nil
}

// WithComponents installs components during NewAgent. It is the construction-time
// equivalent of Agent.With: components install in order and the first error aborts
// construction.
func WithComponents(cs ...Component) Option {
	return func(a *Agent) error {
		return a.With(cs...)
	}
}
