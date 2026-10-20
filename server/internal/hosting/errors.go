package hosting

// UserError is a friendly, user-facing failure (what / why / what-to-do),
// ported from Support\UserFacingException. The What is shown to the user; Why
// and Fix add context. Technical detail stays in logs, never here.
type UserError struct {
	What string
	Why  string
	Fix  string
}

func (e *UserError) Error() string { return e.What }

// ValidationError carries per-field messages for a rejected form, ported from
// Support\ValidationException. Callers re-render the form with these.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return "validation failed" }

// Add records a field error.
func (e *ValidationError) Add(field, message string) {
	if e.Fields == nil {
		e.Fields = map[string]string{}
	}
	e.Fields[field] = message
}

// Any reports whether any field failed.
func (e *ValidationError) Any() bool { return len(e.Fields) > 0 }
