package pb

import (
	"net/netip"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/brunoga/deep/examples/incident/model"
)

// FromModel renders an incident in its external form. Field names and value
// encodings line up with the Go model's JSON on purpose: a patch built
// against either representation addresses the same paths.
func FromModel(inc model.Incident) *Incident {
	out := &Incident{
		Id:        inc.ID,
		Title:     inc.Title,
		Severity:  int32(inc.Severity),
		Status:    string(inc.Status),
		Commander: inc.Commander,
		Services:  inc.Services,
	}
	for _, h := range inc.Hosts {
		out.Hosts = append(out.Hosts, h.String())
	}
	for _, t := range inc.Tasks {
		out.Tasks = append(out.Tasks, &Task{Id: t.ID, Text: t.Text, Owner: t.Owner, Done: t.Done})
	}
	if !inc.Updated.IsZero() {
		out.Updated = timestamppb.New(inc.Updated)
	}
	return out
}

// ToModel converts back. Unparseable host strings are dropped rather than
// failing the whole record: the external form is advisory input, not the
// system of record.
func ToModel(in *Incident) model.Incident {
	out := model.Incident{
		ID:        in.GetId(),
		Title:     in.GetTitle(),
		Severity:  model.Severity(in.GetSeverity()),
		Status:    model.Status(in.GetStatus()),
		Commander: in.GetCommander(),
		Services:  in.GetServices(),
	}
	for _, h := range in.GetHosts() {
		if addr, err := netip.ParseAddr(h); err == nil {
			out.Hosts = append(out.Hosts, addr)
		}
	}
	for _, t := range in.GetTasks() {
		out.Tasks = append(out.Tasks, model.Task{
			ID: t.GetId(), Text: t.GetText(), Owner: t.GetOwner(), Done: t.GetDone(),
		})
	}
	if in.GetUpdated() != nil {
		out.Updated = in.GetUpdated().AsTime()
	}
	return out
}
