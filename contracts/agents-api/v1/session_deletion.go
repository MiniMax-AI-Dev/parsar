package v1

type SessionDeleted struct {
	ID      string `json:"id" binding:"required"`
	Deleted bool   `json:"deleted" binding:"required"`
	Object  string `json:"object" enums:"agent.session.deleted" binding:"required"`
}
