package evaluate

func retainedLimited(limits RetentionLimits) (*retainedEngine, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	p := newRetainedEngine()
	p.retention = limits
	return p, nil
}
