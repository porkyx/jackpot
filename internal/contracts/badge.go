package contracts

// BadgeCategory is presentation/classification metadata. It never changes the
// fixed/semi-fixed/anonymous identity namespace used to deduplicate participants.
type BadgeCategory string

const (
	BadgeFixed       BadgeCategory = "fixed"
	BadgeSemiFixed   BadgeCategory = "semi_fixed"
	BadgeMainManager BadgeCategory = "main_manager"
	BadgeSubManager  BadgeCategory = "sub_manager"
	BadgeNewAccount  BadgeCategory = "new_account"
	BadgeAnonymous   BadgeCategory = "anonymous"
)

func BadgeCategories() [6]BadgeCategory {
	return [6]BadgeCategory{BadgeFixed, BadgeSemiFixed, BadgeMainManager, BadgeSubManager, BadgeNewAccount, BadgeAnonymous}
}

func (category BadgeCategory) Validate() error {
	switch category {
	case BadgeFixed, BadgeSemiFixed, BadgeMainManager, BadgeSubManager, BadgeNewAccount, BadgeAnonymous:
		return nil
	default:
		return NewFault(InvalidInput)
	}
}

func ResolveBadgeCategory(category BadgeCategory, identityKind string) (BadgeCategory, error) {
	if category == "" {
		switch identityKind {
		case "fixed":
			return BadgeFixed, nil
		case "semi_fixed":
			return BadgeSemiFixed, nil
		case "anonymous":
			return BadgeAnonymous, nil
		default:
			return "", NewFault(InvalidInput)
		}
	}
	if category.Validate() != nil || identityKind != "fixed" && identityKind != "semi_fixed" && identityKind != "anonymous" ||
		category == BadgeAnonymous && identityKind != "anonymous" || category != BadgeAnonymous && identityKind == "anonymous" ||
		category == BadgeFixed && identityKind != "fixed" || category == BadgeSemiFixed && identityKind != "semi_fixed" {
		return "", NewFault(InvalidInput)
	}
	return category, nil
}

type BadgeRule struct {
	Excluded bool   `json:"excluded"`
	Weight   uint32 `json:"weight"`
}

type BadgeRules struct {
	WeightingEnabled bool      `json:"weightingEnabled"`
	Fixed            BadgeRule `json:"fixed"`
	SemiFixed        BadgeRule `json:"semiFixed"`
	MainManager      BadgeRule `json:"mainManager"`
	SubManager       BadgeRule `json:"subManager"`
	NewAccount       BadgeRule `json:"newAccount"`
	Anonymous        BadgeRule `json:"anonymous"`
}

func DefaultBadgeRules() BadgeRules {
	rule := BadgeRule{Weight: 100}
	return BadgeRules{Fixed: rule, SemiFixed: rule, MainManager: rule, SubManager: rule, NewAccount: rule, Anonymous: rule}
}

func CloneBadgeRules(rules *BadgeRules) *BadgeRules {
	if rules == nil {
		return nil
	}
	copy := *rules
	return &copy
}

func (rules *BadgeRules) Rule(category BadgeCategory) (BadgeRule, error) {
	if category.Validate() != nil {
		return BadgeRule{}, NewFault(InvalidInput)
	}
	if rules == nil {
		return BadgeRule{Weight: 100}, nil
	}
	switch category {
	case BadgeFixed:
		return rules.Fixed, nil
	case BadgeSemiFixed:
		return rules.SemiFixed, nil
	case BadgeMainManager:
		return rules.MainManager, nil
	case BadgeSubManager:
		return rules.SubManager, nil
	case BadgeNewAccount:
		return rules.NewAccount, nil
	case BadgeAnonymous:
		return rules.Anonymous, nil
	}
	panic("validated badge category has no rule")
}

func (rules *BadgeRules) Validate() error {
	if rules == nil {
		return nil
	}
	positive := false
	for _, category := range BadgeCategories() {
		rule, err := rules.Rule(category)
		if err != nil || rule.Weight > 100 {
			return NewFault(InvalidInput)
		}
		positive = positive || rule.Weight > 0
	}
	if rules.WeightingEnabled && !positive {
		return NewFault(InvalidInput)
	}
	return nil
}
