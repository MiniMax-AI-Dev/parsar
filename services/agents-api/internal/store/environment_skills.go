package store

import (
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
)

// InlineSkill is confidential immutable initialization input. Only Metadata is public.
type InlineSkill struct {
	Metadata agentskill.Metadata `json:"metadata"`
	Archive  []byte              `json:"archive"`
}

const MaxSkillsArchiveBytes = 10 << 20

func ValidateInlineSkills(skills []InlineSkill) error {
	if len(skills) > 50 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	total := 0
	expanded := 0
	for _, skill := range skills {
		total += len(skill.Archive)
		if total > MaxSkillsArchiveBytes || seen[skill.Metadata.Name] {
			return ErrInvalidInput
		}
		seen[skill.Metadata.Name] = true
		files, err := agentskill.Read(skill.Archive, skill.Metadata)
		if err != nil {
			return ErrInvalidInput
		}
		for _, file := range files {
			expanded += len(file.Data)
		}
		if expanded > 50<<20 {
			return ErrInvalidInput
		}
	}
	return nil
}

func (s EnvironmentSetup) SkillMetadata() []agentskill.Metadata {
	result := make([]agentskill.Metadata, 0, len(s.Skills))
	for _, skill := range s.Skills {
		result = append(result, skill.Metadata)
	}
	return result
}

func (s *Store) sealTemplateSkills(tenant, id string, setup EnvironmentSetup) ([]byte, []byte, error) {
	metadata, err := json.Marshal(setup.SkillMetadata())
	if err != nil {
		return nil, nil, err
	}
	contents, err := s.sealEnvironmentSetup(tenant, "environment_template", id, "skills", setup.Skills, len(setup.Skills) == 0)
	return metadata, contents, err
}
