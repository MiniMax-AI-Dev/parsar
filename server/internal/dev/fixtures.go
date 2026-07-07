package dev

const (
	DevVerificationCode = "888888"
	DevAdminEmail       = "admin@example.com"
)

type SeedData struct {
	Workspace     Workspace      `json:"workspace"`
	Users         []User         `json:"users"`
	Agents        []Agent        `json:"agents"`
	Conversations []Conversation `json:"conversations"`
}

type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

type Agent struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	Description string   `json:"description"`
	Skills      []string `json:"skills"`
}

type Conversation struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Visibility string `json:"visibility"`
}

func DefaultSeed() SeedData {
	return SeedData{
		Workspace: Workspace{ID: "dev_workspace", Name: "Demo Workspace", Slug: "demo"},
		Users: []User{
			{ID: "dev_admin", Email: DevAdminEmail, Name: "Dev Admin", Role: "owner"},
		},
		Agents: []Agent{
			{ID: "agent_product", Name: "Product Agent", Slug: "product-agent", Description: "Product-perspective review of requirements and scope", Skills: []string{"prd-review", "scope"}},
			{ID: "agent_backend", Name: "Backend Agent", Slug: "backend-agent", Description: "Backend-perspective review of architecture and data model", Skills: []string{"go", "postgres", "api"}},
			{ID: "agent_test", Name: "TestAgent", Slug: "test-agent", Description: "Test-perspective acceptance and counterexamples", Skills: []string{"e2e", "regression"}},
		},
		Conversations: []Conversation{
			{ID: "conv_demo_group", Title: "Demo Group", Visibility: "workspace"},
		},
	}
}
