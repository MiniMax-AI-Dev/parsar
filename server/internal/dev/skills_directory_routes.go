package dev

import "net/http"

// listSkillsDirectoryInstalls returns persisted Skills.sh installation identities.
//
//	@Summary	List installed Skills.sh skills
//	@Tags		capabilities
//	@ID			listSkillsDirectoryInstalls
//	@Produce	json
//	@Param		workspaceID	path	string	true	"Workspace UUID"
//	@Success	200	{object}	map[string]string	"Registry ID to workspace capability ID"
//	@Failure	400	{object}	map[string]string
//	@Failure	401	{object}	map[string]string
//	@Failure	403	{object}	map[string]string
//	@Failure	404	{object}	map[string]string
//	@Failure	500	{object}	map[string]string
//	@Failure	503	{object}	map[string]string
//	@Router		/api/v1/workspaces/{workspaceID}/skills/installed [get]
func listSkillsDirectoryInstalls(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityRead(w, r, runtimeStore)
		if !ok {
			return
		}
		installs, err := runtimeStore.ListSkillsDirectoryInstalls(r.Context(), workspaceID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list installed skills")
			return
		}
		writeJSON(w, http.StatusOK, installs)
	}
}
