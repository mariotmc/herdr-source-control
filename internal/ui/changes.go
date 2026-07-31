package ui

import (
	"bytes"

	"github.com/mariotmc/herdr-source-control/internal/domain"
)

type ChangeIdentity struct {
	Group domain.Group
	Path  string
}

type ChangeRow struct {
	Heading  bool
	Group    domain.Group
	Count    int
	Resource domain.Resource
	Identity ChangeIdentity
}

func Rows(changes []domain.Change) []ChangeRow {
	groups := domain.ProjectChanges(changes)
	var rows []ChangeRow
	for _, group := range []domain.Group{domain.GroupMerge, domain.GroupStaged, domain.GroupChanges} {
		resources := groups[group]
		if len(resources) == 0 {
			continue
		}
		rows = append(rows, ChangeRow{Heading: true, Group: group, Count: len(resources)})
		for _, resource := range resources {
			rows = append(rows, ChangeRow{
				Group: group, Resource: resource,
				Identity: ChangeIdentity{Group: group, Path: string(resource.Path)},
			})
		}
	}
	return rows
}

func Selectable(rows []ChangeRow) []ChangeIdentity {
	identities := make([]ChangeIdentity, 0, len(rows))
	for _, row := range rows {
		if !row.Heading {
			identities = append(identities, row.Identity)
		}
	}
	return identities
}

func Find(rows []ChangeRow, identity ChangeIdentity) int {
	for index, row := range rows {
		if !row.Heading && row.Group == identity.Group && bytes.Equal(row.Resource.Path, []byte(identity.Path)) {
			return index
		}
	}
	return -1
}

func DisplayPath(resource domain.Resource) string {
	path := EscapeBytes(resource.Path)
	if len(resource.OriginalPath) > 0 && !bytes.Equal(resource.OriginalPath, resource.Path) {
		return EscapeBytes(resource.OriginalPath) + " -> " + path
	}
	return path
}

func StatusLabel(resource domain.Resource, compact bool) string {
	if resource.Untracked {
		if compact {
			return "U"
		}
		return "Untracked"
	}
	if compact {
		return resource.Status.Code()
	}
	if resource.Status == domain.StatusUnknown && resource.Status.Code() != "" {
		code := resource.Status.Code()
		if resource.RawStatus >= 0x20 && resource.RawStatus < 0x7f {
			code = string(resource.RawStatus)
		}
		return "Unknown (" + code + ")"
	}
	return resource.Status.Label()
}
