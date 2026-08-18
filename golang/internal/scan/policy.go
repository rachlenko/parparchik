package scan

import "fmt"

// Policy decides whether a scan Result is acceptable.
type Policy struct {
	// MaxSeverity is the highest Severity that is still allowed. A finding
	// strictly above this severity causes Evaluate to deny. SeverityUnknown
	// findings are always allowed through on their own (an unscored finding
	// isn't evidence of anything past "a database entry exists") — callers
	// wanting stricter handling should treat Vulnerable()==true specially
	// regardless of severity.
	MaxSeverity Severity
}

// Decision is the outcome of evaluating a Result against a Policy.
type Decision struct {
	Allowed bool
	Reason  string
}

// Evaluate applies p to result.
func (p Policy) Evaluate(result Result) Decision {
	if !result.Vulnerable() {
		return Decision{Allowed: true, Reason: "no known vulnerabilities found"}
	}

	worst := result.MaxSeverity()
	if worst > p.MaxSeverity {
		return Decision{
			Allowed: false,
			Reason:  fmt.Sprintf("%d finding(s), worst severity %s exceeds policy max %s", len(result.Findings), worst, p.MaxSeverity),
		}
	}
	return Decision{
		Allowed: true,
		Reason:  fmt.Sprintf("%d finding(s), worst severity %s is within policy max %s", len(result.Findings), worst, p.MaxSeverity),
	}
}

func (s Severity) String() string {
	switch s {
	case SeverityLow:
		return "LOW"
	case SeverityMedium:
		return "MEDIUM"
	case SeverityHigh:
		return "HIGH"
	case SeverityCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}
