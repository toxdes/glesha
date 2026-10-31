package app_cmd

import (
	"errors"
	"fmt"
	"strings"

	"glesha/backup"
	"glesha/database/model"
	L "glesha/logger"
)

type catalogFailureGroup struct {
	message    string
	sets       []string
	operations []string
}

type catalogFailures struct {
	groups []catalogFailureGroup
	causes []error
}

func (f *catalogFailures) add(message string, set, operation string, err error) {
	f.causes = append(f.causes, err)
	index := -1
	for i := range f.groups {
		if f.groups[i].message == message {
			index = i
			break
		}
	}
	if index < 0 {
		f.groups = append(f.groups, catalogFailureGroup{message: message})
		index = len(f.groups) - 1
	}
	group := &f.groups[index]
	if set != "" {
		group.sets = append(group.sets, set)
	}
	if operation != "" {
		group.operations = append(group.operations, operation)
	}
}

func (f *catalogFailures) addSet(r *runtime, set model.Set, err error) error {
	name := set.Name
	if name == "" {
		name = set.ID
	}
	f.add(err.Error(), name, "", fmt.Errorf("%s: %w", set.ID, err))
	if r.env.jsonOutput() {
		return r.emit("catalog_state", catalogSyncResult{Set: set.ID, State: backup.CatalogStateFailed, Error: err.Error()}, "")
	}
	return nil
}

func (f *catalogFailures) addOperation(r *runtime, operation string, err error) error {
	if err == nil {
		return nil
	}
	f.add(err.Error(), "", operation, err)
	if r.env.jsonOutput() {
		return r.emit("catalog_sync_error", struct {
			Operation string `json:"operation"`
			Error     string `json:"error"`
		}{operation, err.Error()}, "")
	}
	return nil
}

func (f *catalogFailures) finish(r *runtime, summary catalogSyncSummary) error {
	r.finishCatalogCheck()
	if !r.env.jsonOutput() {
		for _, group := range f.groups {
			label := strings.Join(group.operations, ", ")
			if len(group.sets) == 1 {
				label = group.sets[0]
			}
			if len(group.sets) > 1 {
				label = fmt.Sprintf("%d catalogs", len(group.sets))
			}
			if len(group.sets) > 0 && len(group.operations) > 0 {
				label += "; " + strings.Join(group.operations, ", ")
			}
			text := label + ": " + strings.TrimPrefix(group.message, "cli: ")
			if err := r.emitAtLevel("catalog_sync_error", nil, text, L.ERROR); err != nil {
				return errors.Join(errors.Join(f.causes...), err)
			}
		}
	}
	if err := r.emitCatalogSummary(summary); err != nil {
		return errors.Join(errors.Join(f.causes...), err)
	}
	if cause := errors.Join(f.causes...); cause != nil {
		return reportedError("catalog sync", cause)
	}
	return nil
}
