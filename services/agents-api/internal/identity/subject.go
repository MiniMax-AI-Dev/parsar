package identity

import "errors"

// Subject identifies a user or service account within an execution project.
type Subject struct {
	Kind string
	ID   string
}

func (s Subject) Validate() error {
	if (s.Kind != "user" && s.Kind != "service_account") || !validID(s.ID) {
		return errors.New("caller subject_kind must be user or service_account with a nonempty subject_id")
	}
	return nil
}

func (p Principal) Subject() Subject { return Subject{Kind: p.SubjectKind, ID: p.SubjectID} }
