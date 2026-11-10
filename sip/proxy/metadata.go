package proxy

import (
	"log/slog"
	"math"

	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// MetadataKey is the [sip.MessageMetadata] key under which the proxy
// stores the serializable [Metadata] of a processed message.
const MetadataKey = "sip.proxy"

// Stage names the proxy processing phase a [Metadata] value describes.
// Correlation fields are populated per stage only when the stamped
// boundary actually knows them; unknown fields stay empty rather than
// being invented.
type Stage string

const (
	// StageClaim marks the route-claim phase of an inbound request: the
	// stamp records the minted request identity, the claiming route, and
	// the effective mode; target and attempt stay empty.
	StageClaim Stage = "claim"
	// StageForward marks request forwarding toward the resolved targets:
	// the stamp of one owned downstream copy carries the request identity
	// plus the target and physical-attempt identities of that send.
	StageForward Stage = "forward"
	// StageResponse marks downstream response processing: an attributed
	// response carries the owning request, target, and attempt
	// identities, while a response without recoverable provenance records
	// only mode and stage.
	StageResponse Stage = "response"
	// StageUpstream marks upstream-facing processing of a request attempt:
	// the stamp carries the request identity plus the single-attempt
	// source only when one attempt provably owns the copy — a local or
	// aggregated upstream response leaves both empty.
	StageUpstream Stage = "upstream"
)

// supported reports whether s names one of the defined stages.
func (s Stage) supported() bool {
	switch s {
	case StageClaim, StageForward, StageResponse, StageUpstream:
		return true
	default:
		return false
	}
}

// Metadata is the serializable proxy state attached to a message envelope
// under [MetadataKey].
//
// A stored value travels inside [sip.MessageMetadata]: it survives
// envelope cloning and JSON serialization while never appearing in the
// rendered SIP message. It is local, non-authenticated state recorded by
// the processing proxy; a value decoded from received metadata asserts
// only what the producing application stored.
//
// Each stamp describes the processing boundary that produced it: the
// [Stage] names the boundary and the remaining fields carry the
// correlation that boundary actually knows. Stage boundaries replace
// rather than merge — [SetMetadata] stores the whole value — so a
// message that traverses the proxy again carries only the fresh
// traversal's record: the stamp is the current boundary, not a history
// of traversals.
//
// Serialization keeps [Metadata.Mode] in its numeric [ForwardMode] form
// (0 stateful, 1 stateless); the readable names are the
// [ForwardMode.String] rendering used in logs, not the JSON contract.
//
// The stamp is observation data with explicit non-goals: it is not
// cross-network transport, not a wire identifier, not authentication,
// and not routing authority. Events, errors, and outcomes join to a
// stamp through the shared [Event.RequestID].
type Metadata struct {
	// RequestID is the proxy request identifier of the processed request,
	// empty when the boundary has no request association. Events, errors,
	// and outcomes of the same request join through it.
	RequestID string `json:"request_id,omitempty"`
	// RelatedRequestID identifies a related request, such as the INVITE a
	// CANCEL belongs to, recorded only when the relationship is really
	// known.
	RelatedRequestID string `json:"related_request_id,omitempty"`
	// TargetID is the logical target of the described attempt, empty when
	// the boundary knows none.
	TargetID string `json:"target_id,omitempty"`
	// AttemptID identifies a concrete downstream attempt, empty when the
	// boundary owns no single-attempt source.
	AttemptID string `json:"attempt_id,omitempty"`
	// RouteName is the name of the claiming route, empty when the
	// boundary has no route association.
	RouteName string `json:"route_name,omitempty"`
	// Mode is the forwarding mode of the claiming route.
	Mode ForwardMode `json:"mode"`
	// Stage is the processing phase the value describes.
	Stage Stage `json:"stage"`
}

// LogValue renders md as one [slog.GroupValue] so correlation-aware
// logs record the whole stamp through a single attribute, for example
// slog.Any("proxy", md).
//
// The group keys mirror the serialized field names in a stable order —
// request_id, related_request_id, target_id, attempt_id, route_name,
// mode, and stage — with empty string fields omitted rather than
// rendered as placeholders. Mode renders through [ForwardMode.String]
// only for a supported value: String() names every unsupported value
// "stateful", so an invalid mode keeps its numeric form rather than
// being mislabeled.
func (md Metadata) LogValue() slog.Value {
	attrs := make([]slog.Attr, 0, 7)
	for _, f := range []struct {
		key string
		val string
	}{
		{"request_id", md.RequestID},
		{"related_request_id", md.RelatedRequestID},
		{"target_id", md.TargetID},
		{"attempt_id", md.AttemptID},
		{"route_name", md.RouteName},
	} {
		if f.val != "" {
			attrs = append(attrs, slog.String(f.key, f.val))
		}
	}
	if md.Mode.supported() {
		attrs = append(attrs, slog.String("mode", md.Mode.String()))
	} else {
		attrs = append(attrs, slog.Uint64("mode", uint64(md.Mode)))
	}
	attrs = append(attrs, slog.String("stage", string(md.Stage)))
	return slog.GroupValue(attrs...)
}

// MetadataFrom reads and validates the [Metadata] stored under
// [MetadataKey].
//
// It accepts a native [Metadata] value or the map[string]any shape
// produced by JSON-decoding a serialized one. Decoded maps require a
// supported mode and stage; known fields must carry values of their
// declared types — a null or wrongly typed known field rejects the whole
// value — while unknown fields are ignored so newer producers stay
// readable. The source map is never mutated.
//
// A valid record may be partial: correlation fields stay empty when the
// producing boundary knew none.
//
// It returns the zero Metadata and false when the container is nil, the
// key is absent, or the stored value fails validation.
func MetadataFrom(meta *sip.MessageMetadata) (Metadata, bool) {
	val, ok := meta.Get(MetadataKey)
	if !ok {
		return Metadata{}, false
	}

	switch v := val.(type) {
	case Metadata:
		return validMeta(v)
	case map[string]any:
		return metaFromMap(v)
	default:
		return Metadata{}, false
	}
}

// SetMetadata stores value under [MetadataKey], replacing any previously
// stored proxy metadata with the whole given value — stamps never merge,
// so a repeated traversal records only the fresh boundary's record.
// A nil container is a no-op.
func SetMetadata(meta *sip.MessageMetadata, value Metadata) {
	if meta == nil {
		return
	}
	meta.Set(MetadataKey, value)
}

// stampClaim records the claim-stage [Metadata] of one claimed request
// on its envelope. The stored value replaces any previously recorded
// proxy metadata: a fresh traversal stamps a new claim record rather
// than merging with a predecessor's. A nil envelope is a no-op.
func stampClaim(env *sip.RequestEnvelope, md Metadata) {
	if env == nil {
		return
	}
	SetMetadata(env.Metadata(), md)
}

// stampForward records the forward-stage [Metadata] of one owned
// downstream request copy on its envelope. The stored value replaces the
// claim record the clone inherited: the copy describes the physical
// attempt that sends it rather than the inbound claim it was prepared
// from. A nil envelope is a no-op.
func stampForward(env *sip.RequestEnvelope, md Metadata) {
	if env == nil {
		return
	}
	SetMetadata(env.Metadata(), md)
}

// linkRelatedReq records a really-known live parent identity on the
// claim record of a related request the proxy actually processed — a
// matched CANCEL or a related method that reached forwarding. The
// child's own claim identity is preserved (minted when the envelope
// somehow carries no usable record), while target and attempt stay
// empty: they belong on the known downstream copies, never on the
// inbound claim. The parent's own envelopes are never touched. A nil
// envelope or an empty parent identity is a no-op.
func linkRelatedReq(route *Route, req *sip.RequestEnvelope, relatedID string) {
	if req == nil || relatedID == "" {
		return
	}
	md, ok := MetadataFrom(req.Metadata())
	if !ok {
		md = Metadata{Stage: StageClaim}
		if route != nil {
			md.RouteName = route.Name
			md.Mode = route.ForwardMode
		}
	}
	if md.RequestID == "" {
		// A claimed request always carries the minted claim identity; an
		// unstamped envelope still gets a usable lightweight one.
		md.RequestID = util.RandString(24)
	}
	md.RelatedRequestID = relatedID
	md.TargetID = ""
	md.AttemptID = ""
	md.Stage = StageClaim
	stampClaim(req, md)
}

// stampRes records a response-side [Metadata] value on an owned
// response copy: inbound attribution uses the response stage, an upstream
// send the upstream stage. The whole stored value replaces the stage
// record the clone inherited. A nil envelope is a no-op.
func stampRes(res *sip.ResponseEnvelope, md Metadata) {
	if res == nil {
		return
	}
	SetMetadata(res.Metadata(), md)
}

// stageMetaLocked builds the [Metadata] one stage stamp records under
// the coordinator guard: the request identity and the claiming route are
// always known, while target and attempt correlation are recorded only
// when the stamped boundary actually owns one — a local or aggregated
// upstream response keeps both empty rather than inventing a winner.
func (rq *Request) stageMetaLocked(stage Stage, targetID, attID string) Metadata {
	md := Metadata{
		RequestID: rq.id,
		TargetID:  targetID,
		AttemptID: attID,
		Stage:     stage,
	}
	if rq.route != nil {
		md.RouteName = rq.route.Name
		md.Mode = rq.route.ForwardMode
	}
	return md
}

// stageMeta is the locking variant of [Request.stageMetaLocked]
// for callers outside the coordinator guard.
func (rq *Request) stageMeta(stage Stage, targetID, attID string) Metadata {
	rq.mu.Lock()
	defer rq.mu.Unlock()
	return rq.stageMetaLocked(stage, targetID, attID)
}

// claimID returns the operation identity recorded by the claim stamp
// of env, or an empty string when the envelope carries no valid claim
// stamp.
func claimID(env *sip.RequestEnvelope) string {
	if env == nil {
		return ""
	}
	md, ok := MetadataFrom(env.Metadata())
	if !ok || md.Stage != StageClaim {
		return ""
	}
	return md.RequestID
}

// validMeta returns md when both enumerations hold supported values.
func validMeta(md Metadata) (Metadata, bool) {
	if !md.Mode.supported() || !md.Stage.supported() {
		return Metadata{}, false
	}
	return md, true
}

// metaFromMap decodes a deserialized [Metadata] map. Unknown fields
// are ignored; absent optional string fields stay empty.
func metaFromMap(m map[string]any) (Metadata, bool) {
	var md Metadata

	mode, ok := metaMode(m["mode"])
	if !ok {
		return Metadata{}, false
	}
	md.Mode = mode

	stage, ok := m["stage"].(string)
	if !ok {
		return Metadata{}, false
	}
	md.Stage = Stage(stage)

	if !md.Stage.supported() {
		return Metadata{}, false
	}

	strFields := []struct {
		key string
		dst *string
	}{
		{"request_id", &md.RequestID},
		{"related_request_id", &md.RelatedRequestID},
		{"target_id", &md.TargetID},
		{"attempt_id", &md.AttemptID},
		{"route_name", &md.RouteName},
	}
	for _, f := range strFields {
		raw, ok := m[f.key]
		if !ok {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			return Metadata{}, false
		}
		*f.dst = s
	}

	return md, true
}

// metaMode converts a decoded mode field to a supported
// [ForwardMode]. Only non-negative integral numbers name a mode; strings,
// nulls, fractions, and out-of-range values are rejected — a malformed
// mode never defaults to stateful.
func metaMode(v any) (ForwardMode, bool) {
	var n float64
	switch num := v.(type) {
	case float32:
		n = float64(num)
	case float64:
		n = num
	case int:
		n = float64(num)
	case int8:
		n = float64(num)
	case int16:
		n = float64(num)
	case int32:
		n = float64(num)
	case int64:
		n = float64(num)
	case uint:
		n = float64(num)
	case uint8:
		n = float64(num)
	case uint16:
		n = float64(num)
	case uint32:
		n = float64(num)
	case uint64:
		n = float64(num)
	default:
		return 0, false
	}

	// Truncation before the conversion would silently accept fractional
	// and overflowing values, so validate the number first.
	if n != math.Trunc(n) || n < 0 || n > math.MaxUint64 {
		return 0, false
	}
	mode := ForwardMode(n)
	return mode, mode.supported()
}
