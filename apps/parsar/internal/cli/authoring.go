package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func runWorkspace(ctx *runContext, args []string) error {
	if len(args) == 1 && args[0] == "context" {
		return runAuthoring(ctx, proto.AuthoringRequestPayload{Operation: proto.AuthoringContext})
	}
	fmt.Fprintln(ctx.stdout, "Usage: parsar workspace context")
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return nil
	}
	return fmt.Errorf("workspace: expected context")
}

func runSkill(ctx *runContext, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(ctx.stdout, "Usage: parsar skill list | get <id> | create --file /path/SKILL.md | update --file /path/SKILL.md <id>")
		return nil
	}
	operations := map[string]string{"list": proto.AuthoringSkillList, "get": proto.AuthoringSkillRead, "create": proto.AuthoringSkillCreate, "update": proto.AuthoringSkillUpdate}
	operation, ok := operations[args[0]]
	if !ok {
		return fmt.Errorf("skill: unknown subcommand %q", args[0])
	}
	fs := newFlagSet("skill " + args[0])
	file := fs.String("file", "", "absolute path to Skill Markdown")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	request := proto.AuthoringRequestPayload{Operation: operation}
	needsID := args[0] == "get" || args[0] == "update"
	if needsID {
		if fs.NArg() != 1 {
			return fmt.Errorf("skill %s: exactly one capability ID is required", args[0])
		}
		request.CapabilityID = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return fmt.Errorf("skill %s: unexpected arguments", args[0])
	}
	if args[0] == "create" || args[0] == "update" {
		content, err := readAuthoringFile(*file)
		if err != nil {
			return err
		}
		request.Content = content
	} else if *file != "" {
		return fmt.Errorf("skill %s does not accept --file", args[0])
	}
	return runAuthoring(ctx, request)
}

func runAgent(ctx *runContext, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(ctx.stdout, "Usage: parsar agent instructions get | set --file /path/instructions.md")
		return nil
	}
	if len(args) < 2 || args[0] != "instructions" {
		return fmt.Errorf("agent: expected instructions get or set")
	}
	if args[1] == "get" && len(args) == 2 {
		return runAuthoring(ctx, proto.AuthoringRequestPayload{Operation: proto.AuthoringPromptRead})
	}
	if args[1] != "set" {
		return fmt.Errorf("agent instructions: expected get or set")
	}
	fs := newFlagSet("agent instructions set")
	file := fs.String("file", "", "absolute path to replacement instructions")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("agent instructions set: unexpected arguments")
	}
	content, err := readAuthoringFile(*file)
	if err != nil {
		return err
	}
	return runAuthoring(ctx, proto.AuthoringRequestPayload{Operation: proto.AuthoringPromptWrite, Content: content})
}

func readAuthoringFile(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("--file must be an absolute path or start with ~/")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, proto.AuthoringMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > proto.AuthoringMaxBytes {
		return "", fmt.Errorf("authoring file exceeds 1 MiB")
	}
	return string(data), nil
}

func runAuthoring(ctx *runContext, request proto.AuthoringRequestPayload) error {
	path := os.Getenv(proto.AuthoringSocketEnv)
	if !filepath.IsAbs(path) {
		return fmt.Errorf("workspace commands require an active run on a daemon supporting workspace authoring")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(data) > proto.AuthoringMaxBytes {
		return fmt.Errorf("encoded authoring request exceeds 1 MiB")
	}
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect to this run's daemon: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.Copy(conn, bytes.NewReader(append(data, '\n'))); err != nil {
		return err
	}
	var response proto.AuthoringResponsePayload
	if err := json.NewDecoder(io.LimitReader(conn, proto.AuthoringMaxBytes+1)).Decode(&response); err != nil {
		return fmt.Errorf("daemon response unavailable; check whether the operation completed before retrying: %w", err)
	}
	if response.Error != "" {
		return fmt.Errorf("workspace operation failed: %s", response.Error)
	}
	return emitJSON(ctx.stdout, response.Data)
}
