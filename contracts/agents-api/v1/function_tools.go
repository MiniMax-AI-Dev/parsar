package v1

import "encoding/json"

// FunctionToolInput is the supported application-defined tool configuration.
type FunctionToolInput struct {
	Type         string          `json:"type" enums:"function" binding:"required"`
	Name         *string         `json:"name" binding:"required"`
	Description  *string         `json:"description" binding:"required"`
	Parameters   json.RawMessage `json:"parameters" swaggertype:"object" binding:"required"`
	DeferLoading json.RawMessage `json:"defer_loading,omitempty" swaggertype:"boolean"`
}
