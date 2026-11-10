package crud

import "github.com/awesome-goose/goose/openapi"

// Updated is what PATCH /:id answers: the id of the row it changed.
type Updated struct {
	ID string `json:"id" binding:"required"`
}

// OpenAPIFor documents the resource handlers a controller gets from CrudResource, whose shared
// Dtos cannot name the entity (Goose openapi.ControllerDocumented, PLAN U10/U25): List answers
// the entities ([]E; paging is in the envelope's meta), Get, Create and PUT the entity, PATCH
// its id, and Delete nothing. A resource whose After* morphs change the shape documents its own.
func (r *CrudResource[E, C, U]) OpenAPIFor(handler string) openapi.Operation {
	switch handler {
	case "List", "FindByIds", "FindByType":
		return openapi.Operation{Response: []E{}}
	case "Get", "Create", "UpdatePut", "CreateIgnoreDuplicate", "Upsert", "FindRelatives":
		return openapi.Operation{Response: *new(E)}
	case "Update":
		return openapi.Operation{Response: Updated{}}
	}
	return openapi.Operation{}
}
