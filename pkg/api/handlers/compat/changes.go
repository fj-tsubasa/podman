package compat

import (
	"net/http"

	"github.com/containers/podman/v3/libpod"
	"github.com/containers/podman/v3/libpod/define"
	"github.com/containers/podman/v3/pkg/api/handlers/utils"
	"github.com/gorilla/schema"
	"github.com/pkg/errors"
)

func Changes(w http.ResponseWriter, r *http.Request) {
	decoder := r.Context().Value("decoder").(*schema.Decoder)
	runtime := r.Context().Value("runtime").(*libpod.Runtime)

	query := struct {
		Parent   string `schema:"parent"`
		DiffType string `schema:"diffType"`
	}{}
	if err := decoder.Decode(&query, r.URL.Query()); err != nil {
		utils.Error(w, "Something went wrong.", http.StatusBadRequest, errors.Wrapf(err, "failed to parse parameters for %s", r.URL.String()))
		return
	}
	var diffType define.DiffType
	switch query.DiffType {
	case "", "all":
		diffType = define.DiffAll
	case "container":
		diffType = define.DiffContainer
	case "image":
		diffType = define.DiffImage
	default:
		utils.Error(w, "Something went wrong.", http.StatusBadRequest, errors.Errorf("invalid diffType value %q", query.DiffType))
		return
	}

	id := utils.GetName(r)
	changes, err := runtime.GetDiff(query.Parent, id, diffType)
	if err != nil {
		utils.InternalServerError(w, err)
		return
	}
	utils.WriteJSON(w, 200, changes)
}
