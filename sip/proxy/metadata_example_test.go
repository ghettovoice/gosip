package proxy_test

import (
	"encoding/json"
	"fmt"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

// ExampleMetadataFrom reads the proxy processing stamp recorded on a
// message envelope. The proxy stores one serializable proxy.Metadata
// value under proxy.MetadataKey in the envelope's sip.MessageMetadata:
// the value survives envelope cloning and JSON serialization while never
// appearing in the rendered SIP message.
//
// Metadata.Stage names the processing boundary the stamp describes:
// proxy.StageClaim is the route-claim phase of an inbound request,
// proxy.StageForward marks one owned downstream request copy carrying
// the target and physical-attempt identities of its send,
// proxy.StageResponse marks downstream response processing, and
// proxy.StageUpstream marks upstream-facing processing of a request
// attempt. The remaining fields correlate the stamp: RequestID is the
// proxy request identity minted at claim — events, errors, and outcomes
// of the same request join through it — RelatedRequestID links a matched
// CANCEL or a forwarded ACK to its live parent request, TargetID and
// AttemptID name the plan target and the concrete attempt, and RouteName
// and Mode record the claiming route and its effective forward mode.
// Correlation fields are populated only when the stamped boundary
// actually knows them: a stateless response without recoverable
// provenance records only mode and stage, the honest partial record
// {mode: 1, stage: "response"} once decoded.
//
// Mode serializes as the numeric proxy.ForwardMode (0 stateful,
// 1 stateless); the readable "stateful"/"stateless" names are the
// ForwardMode.String rendering used in logs, not the JSON contract.
// proxy.SetMetadata replaces the whole stored value — stamps never merge —
// so a message looping back through the proxy carries only the fresh
// traversal's record rather than accumulating history.
//
// The stamp is local, non-authenticated observation data with explicit
// non-goals: it is not cross-network transport (metadata lives on the
// in-process envelope, not on the wire), not a wire identifier (Via
// branch values and transaction keys remain the wire identities), not
// authentication (a decoded value asserts only what the producing
// application stored), and not routing authority (claim, loop, and Via
// ownership checks never consult it).
func ExampleMetadataFrom() {
	meta := &sip.MessageMetadata{}
	proxy.SetMetadata(meta, proxy.Metadata{
		RequestID: "req-1",
		TargetID:  "alice-desk",
		AttemptID: "alice-desk#1",
		RouteName: "static-location",
		Mode:      proxy.ForwardModeStateful,
		Stage:     proxy.StageForward,
	})

	// A JSON round trip restores the stored stamp as a map[string]any —
	// the shape MetadataFrom also accepts — with Mode in its numeric form.
	data, err := json.Marshal(meta)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	var restored sip.MessageMetadata
	if err := json.Unmarshal(data, &restored); err != nil {
		fmt.Println("error:", err)
		return
	}

	md, ok := proxy.MetadataFrom(&restored)
	fmt.Println(ok, md.RequestID, md.TargetID, md.AttemptID, md.RouteName, md.Mode, md.Stage)

	// A stateless response stamp is honestly partial: only mode and stage
	// are recorded, and its serialized form is {"mode":1,"stage":"response"}.
	partial := &sip.MessageMetadata{}
	proxy.SetMetadata(partial, proxy.Metadata{
		Mode:  proxy.ForwardModeStateless,
		Stage: proxy.StageResponse,
	})
	pdata, err := json.Marshal(partial)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(string(pdata))
	pm, ok := proxy.MetadataFrom(partial)
	fmt.Println(ok, pm.Mode, pm.Stage, pm.RequestID == "")

	// Output:
	// true req-1 alice-desk alice-desk#1 static-location stateful forward
	// {"sip.proxy":{"mode":1,"stage":"response"}}
	// true stateless response true
}
