package api

import (
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type environmentFileOptions struct {
	directory         string
	relativeDirectory string
	limit             int
	ascending         bool
	binding           string
	cursor            *environmentFileCursor
}

func readEnvironmentFileQuery(w http.ResponseWriter, r *http.Request, environment store.Environment) (environmentFileOptions, bool) {
	var options environmentFileOptions
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return options, false
	}
	for key, values := range q {
		if !slices.Contains([]string{"path", "limit", "order", "page"}, key) || len(values) != 1 || values[0] == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "Supported list parameters are path, limit, order and page, each supplied once with a nonempty value.")
			return options, false
		}
	}
	pageQuery := url.Values{}
	for _, key := range []string{"limit", "order"} {
		if values, exists := q[key]; exists {
			pageQuery[key] = values
		}
	}
	page, ok := readPageQuery(w, pageQuery, true)
	if !ok {
		return options, false
	}
	configuration, err := decodeSessionEnvironment(environment.Configuration)
	if err != nil || configuration.Type != "self_hosted" || len(configuration.CapabilityDirectories) != 0 || !validEnvironmentFilePath(configuration.WorkspaceDirectory) {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return options, false
	}
	root := path.Clean(configuration.WorkspaceDirectory)
	directory := root
	if requested, exists := q["path"]; exists {
		if !validEnvironmentFilePath(requested[0]) {
			writeStoreError(w, r, store.ErrInvalidInput)
			return options, false
		}
		directory = path.Clean(requested[0])
	}
	rootPrefix := strings.TrimSuffix(root, "/") + "/"
	if directory != root && !strings.HasPrefix(directory, rootPrefix) {
		writeStoreError(w, r, store.ErrInvalidInput)
		return options, false
	}
	options = environmentFileOptions{directory: directory, limit: page.limit, ascending: page.ascending}
	if directory != root {
		options.relativeDirectory = strings.TrimPrefix(directory, rootPrefix)
	}
	options.binding = environmentFilesDigest([]any{tenantID(r), environment.ID, directory, options.limit, options.ascending})
	if token, exists := q["page"]; exists {
		cursor, err := decodeEnvironmentFileCursor(token[0], options.binding)
		if err != nil {
			writeStoreError(w, r, err)
			return environmentFileOptions{}, false
		}
		options.cursor = &cursor
	}
	return options, true
}

func validEnvironmentFilePath(value string) bool {
	return path.IsAbs(value) && len(value) <= 4096 && utf8.ValidString(value) &&
		!strings.ContainsAny(value, "\\\x00\r\n") && !slices.Contains(strings.Split(value, "/"), "..")
}
