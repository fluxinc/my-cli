package recordsvc

import (
	"strings"

	"github.com/fluxinc/my-cli/internal/fleet"
)

// ToFleet projects a service record into the fleet registry shape. Every
// field is read from a named place in the record; nothing is inferred.
// Identifiers gathers every exact identifier the record carries, because
// support linking and unknown-identifier warnings match on that list.
func ToFleet(serviceID string, r Record) fleet.Record {
	order := obj(r["order"])
	box := obj(r["box"])
	customer := obj(r["customer"])
	rec := fleet.Record{
		ID:           r.ID(),
		Customer:     first(str(customer["workspace_customer_id"]), str(customer["name"])),
		Partner:      str(order["partner"]),
		Status:       str(r["stage"]),
		Device:       str(box["device_model"]),
		Serial:       str(box["serial_number"]),
		ConfigRepo:   str(box["config_repo"]),
		ConfigBranch: str(box["config_branch"]),
		DeployedSite: str(r["site"]),
		ShipTo:       str(r["ship_to"]),
		InstallDate:  str(box["installed_on"]),
		Source:       "service",
		Surface:      "service:" + serviceID,
		URL:          str(r["url"]),
		Archived:     r["archived"] == true,
		Contacts:     contacts(r["contacts"]),
	}
	var ids []string
	for _, key := range []string{"so_number", "po_number", "po_number_2"} {
		ids = append(ids, str(order[key]))
	}
	for _, key := range []string{"serial_number", "label", "hostname", "tailscale_name", "teamviewer_id", "anydesk_id", "zerotier_id", "guid"} {
		ids = append(ids, str(box[key]))
	}
	identifiers := obj(r["identifiers"])
	for _, kind := range sortedKeys(identifiers) {
		for _, item := range list(identifiers[kind]) {
			ids = append(ids, str(obj(item)["value"]))
		}
	}
	for _, item := range list(r["shipments"]) {
		ids = append(ids, str(obj(item)["tracking_number"]))
	}
	for _, item := range list(r["sources"]) {
		ids = append(ids, str(obj(item)["source_id"]))
	}
	rec.Identifiers = uniqueNonEmpty(ids)
	return rec
}

func contacts(value any) []string {
	var out []string
	for _, item := range list(value) {
		c := obj(item)
		name := str(c["name"])
		if name == "" {
			continue
		}
		if role := str(c["role"]); role != "" {
			name = role + ": " + name
		}
		out = append(out, name)
	}
	return out
}

func obj(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	if m, ok := value.(Record); ok {
		return m
	}
	return map[string]any{}
}

func list(value any) []any {
	if l, ok := value.([]any); ok {
		return l
	}
	return nil
}

func first(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		key := strings.ToLower(v)
		if v == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}
