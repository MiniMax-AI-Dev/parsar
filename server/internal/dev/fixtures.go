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
			{ID: "agent_product", Name: "产品Agent", Slug: "product-agent", Description: "产品视角评估需求和范围", Skills: []string{"prd-review", "scope"}},
			{ID: "agent_backend", Name: "后端Agent", Slug: "backend-agent", Description: "后端视角评估架构和数据模型", Skills: []string{"go", "postgres", "api"}},
			{ID: "agent_test", Name: "测试Agent", Slug: "test-agent", Description: "测试视角补充验收和反例", Skills: []string{"e2e", "regression"}},
		},
		Conversations: []Conversation{
			{ID: "conv_demo_group", Title: "Demo Group", Visibility: "workspace"},
		},
	}
}
