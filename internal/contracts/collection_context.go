package contracts

func (context CollectionContext) Validate() error {
	if err := ValidateID(context.BackendSessionID); err != nil {
		return err
	}
	if err := ValidateID(context.CollectionID); err != nil {
		return err
	}
	return ValidateCounter(context.Revision)
}
func (context RoundContext) Validate() error {
	if err := context.CollectionContext.Validate(); err != nil {
		return err
	}
	if err := ValidateID(context.RoundID); err != nil {
		return err
	}
	return ValidateCounter(context.Version)
}
func CheckCollectionContext(current, expected CollectionContext) error {
	if current.Validate() != nil {
		return NewFault(InvalidState)
	}
	if expected.Validate() != nil {
		return NewFault(InvalidInput)
	}
	if current.BackendSessionID != expected.BackendSessionID {
		return NewFault(BackendSessionChanged)
	}
	if current.CollectionID != expected.CollectionID {
		return NewFault(InvalidInput)
	}
	if current.Revision != expected.Revision {
		return NewFault(StaleRevision)
	}
	return nil
}
func CheckRoundContext(current, expected RoundContext) error {
	if err := CheckCollectionContext(current.CollectionContext, expected.CollectionContext); err != nil {
		return err
	}
	if current.Validate() != nil {
		return NewFault(InvalidState)
	}
	if expected.Validate() != nil {
		return NewFault(InvalidInput)
	}
	if current.RoundID != expected.RoundID {
		return NewFault(InvalidInput)
	}
	if current.Version != expected.Version {
		return NewFault(StaleRevision)
	}
	return nil
}
