package config

type CustomLabel struct {
	label    []string
	labelKey map[string]struct{}
}

func NewCustomLabel(targets []TargetConfig) *CustomLabel {
	cl := CustomLabel{
		label:    make([]string, 0),
		labelKey: make(map[string]struct{}),
	}

	for _, t := range targets {
		if t.Labels == nil {
			continue
		}

		for k := range t.Labels {
			if _, ok := cl.labelKey[k]; ok {
				continue
			}
			cl.label = append(cl.label, k)
			cl.labelKey[k] = struct{}{}
		}
	}
	return &cl

}

func (cl *CustomLabel) Labels() []string {
	return cl.label
}

func (cl *CustomLabel) Values(target TargetConfig) []string {
	values := make([]string, len(cl.label))
	if target.Labels == nil {
		return values
	}
	for i, l := range cl.label {
		if _, ok := target.Labels[l]; ok {
			values[i] = target.Labels[l]
		}
	}
	return values
}
