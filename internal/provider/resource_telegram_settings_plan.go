package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// The settings are saved as a PUT of only the configured leaves: SpaceMan stores what is sent and takes its built-in
// default for the rest, so a leaf left out follows SpaceMan's default. The provider records which leaves were
// configured at the last apply (private state), so that taking one out of the configuration plans a change back to
// the default instead of keeping the old value.

const configuredKey = "configured"

// tgLeaf is one settable value: a top-level attribute or a field of one of the nested objects.
type tgLeaf struct {
	key  string // "link_reconfirm_days", "send_limits.queue_total"
	path path.Path
	typ  attr.Type
}

// tgLeaves lists every settable value of the settings.
func tgLeaves(ctx context.Context) []tgLeaf {
	var rs resource.SchemaResponse
	newTelegramSettingsResource().Schema(ctx, resource.SchemaRequest{}, &rs)
	var out []tgLeaf
	for k, a := range rs.Schema.Attributes {
		switch k {
		case "id", "send_limits", "inbound_limits", "notification_defaults":
		default:
			out = append(out, tgLeaf{key: k, path: path.Root(k), typ: a.GetType()})
		}
	}
	for group, attrs := range map[string]map[string]schema.Attribute{
		"send_limits": tgSendLimitsAttrs, "inbound_limits": tgInboundLimitsAttrs, "notification_defaults": tgNotificationAttrs} {
		for k, a := range attrs {
			out = append(out, tgLeaf{key: group + "." + k, path: path.Root(group).AtName(k), typ: a.GetType()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// SpaceMan's built-in defaults of the values that the cross-field rules compare.
const (
	tgDefQueueTotal    = 5000
	tgDefQueuePerChat  = 50
	tgDefIdleSeconds   = 900
	tgDefAbsoluteSecs  = 3600
	tgDefQuietStart    = "23:00"
	tgDefQuietEnd      = "08:00"
	keyQueueTotal      = "send_limits.queue_total"
	keyQueuePerChat    = "send_limits.queue_per_chat"
	keyIdle            = "miniapp_session_idle_seconds"
	keyAbsolute        = "miniapp_session_absolute_seconds"
	keyQuietHoursStart = "notification_defaults.quiet_hours_start"
	keyQuietHoursEnd   = "notification_defaults.quiet_hours_end"
)

type attrGetter interface {
	GetAttribute(ctx context.Context, p path.Path, target interface{}) diag.Diagnostics
}

func getLeaf(ctx context.Context, src attrGetter, l tgLeaf, d *diag.Diagnostics) attr.Value {
	var v attr.Value
	d.Append(src.GetAttribute(ctx, l.path, &v)...)
	return v
}

// configuredLeaves reads which leaves were configured at the last apply.
// recorded is false when there is no record at all (the resource was imported).
func configuredLeaves(ctx context.Context, p privateReader, d *diag.Diagnostics) (out map[string]bool, recorded bool) {
	out = map[string]bool{}
	if p == nil {
		return out, false
	}
	b, dd := p.GetKey(ctx, configuredKey)
	d.Append(dd...)
	var keys []string
	if len(b) > 0 && json.Unmarshal(b, &keys) == nil {
		recorded = true
		for _, k := range keys {
			out[k] = true
		}
	}
	return out, recorded
}

// setConfiguredLeaves records the leaves the configuration sets.
func setConfiguredLeaves(ctx context.Context, p privateWriter, cfg attrGetter, d *diag.Diagnostics) {
	if p == nil {
		return
	}
	keys := []string{}
	for _, l := range tgLeaves(ctx) {
		if v := getLeaf(ctx, cfg, l, d); v != nil && !v.IsNull() {
			keys = append(keys, l.key)
		}
	}
	b, _ := json.Marshal(keys)
	d.Append(p.SetKey(ctx, configuredKey, b)...)
}

// ModifyPlan keeps the settings in step with what SpaceMan will store. A leaf that was configured and no longer is goes
// back to SpaceMan's default; any save stores only the configured leaves, so every leaf left out is then re-read from
// SpaceMan. The rules that span values are checked on the planned values: a side the configuration leaves out is its
// stored value (or its built-in default on create and whenever an update is planned, as it then resets).
func (r *telegramSettingsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return // destroy
	}
	leaves := tgLeaves(ctx)
	configured, recorded := configuredLeaves(ctx, req.Private, &resp.Diagnostics)
	hasState := !req.State.Raw.IsNull()

	type side struct {
		cfg, plan, state attr.Value
	}
	sides := map[string]side{}
	// After an import there is no record of what was configured, so a leaf taken out later could not be told from one
	// that was never set: plan a one-off update, whose apply stores only the configuration and seeds the record.
	// Known gap: an import whose configuration sets every leaf to its current value plans no diff at all, so no apply
	// runs and the record is never seeded; a later leaf taken out is then not reverted. Accepted, because it needs
	// every leaf configured to exactly the stored value right after an import, which is very unlikely.
	changed := hasState && !recorded
	for _, l := range leaves {
		s := side{cfg: getLeaf(ctx, req.Config, l, &resp.Diagnostics), plan: getLeaf(ctx, req.Plan, l, &resp.Diagnostics)}
		if hasState {
			s.state = getLeaf(ctx, req.State, l, &resp.Diagnostics)
		}
		sides[l.key] = s
		if s.cfg == nil || s.plan == nil {
			continue
		}
		switch {
		case s.cfg.IsNull() && configured[l.key]:
			changed = true // taken out of the configuration: back to the default
		case !s.cfg.IsNull() && s.state != nil && !s.plan.Equal(s.state):
			changed = true
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if hasState && changed {
		// The save that follows stores only the configured leaves and SpaceMan's defaults fill the rest, which may have
		// moved since they were read: so they are not predicted.
		for _, l := range leaves {
			if s := sides[l.key]; s.cfg.IsNull() && !s.plan.IsUnknown() {
				unknown, err := l.typ.ValueFromTerraform(ctx, tftypes.NewValue(l.typ.TerraformType(ctx), tftypes.UnknownValue))
				if err != nil {
					resp.Diagnostics.AddError("Could not plan the Telegram settings", err.Error())
					return
				}
				resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, l.path, unknown)...)
			}
		}
	}

	// effective is the value a leaf will have: ok is false when it cannot be known yet. A leaf left out of the
	// configuration keeps its stored value only while no update is planned. Whenever one is (an import, which leaves no
	// record of the configured leaves, plans one; so does taking a leaf out), the save stores only the configured leaves
	// and SpaceMan's defaults fill the rest, so the stored value must not be used. Edge case: after an import where
	// every leaf left out reverts, the checks run on the defaults, not on the imported values.
	effective := func(key string) (attr.Value, bool) {
		s := sides[key]
		switch {
		case s.cfg == nil:
			return nil, false
		case s.cfg.IsUnknown():
			return nil, false
		case !s.cfg.IsNull():
			return s.cfg, true
		case hasState && !changed && s.state != nil && !s.state.IsNull() && !s.state.IsUnknown():
			return s.state, true
		}
		return nil, true // the built-in default
	}
	num := func(key string, def int64) (int64, bool) {
		v, ok := effective(key)
		if !ok {
			return 0, false
		}
		if i, isInt := v.(types.Int64); isInt {
			return i.ValueInt64(), true
		}
		return def, true
	}
	str := func(key, def string) (string, bool) {
		v, ok := effective(key)
		if !ok {
			return "", false
		}
		if s, isStr := v.(types.String); isStr {
			return s.ValueString(), true
		}
		return def, true
	}
	if idle, ok1 := num(keyIdle, tgDefIdleSeconds); ok1 {
		if abs, ok2 := num(keyAbsolute, tgDefAbsoluteSecs); ok2 && abs < idle {
			resp.Diagnostics.AddAttributeError(path.Root(keyAbsolute), "Absolute lifetime shorter than the idle timeout",
				fmt.Sprintf("miniapp_session_absolute_seconds (%d) must be at least miniapp_session_idle_seconds (%d); a value left out is "+
					"SpaceMan's stored value or default (%d and %d).", abs, idle, tgDefAbsoluteSecs, tgDefIdleSeconds))
		}
	}
	if total, ok1 := num(keyQueueTotal, tgDefQueueTotal); ok1 {
		if per, ok2 := num(keyQueuePerChat, tgDefQueuePerChat); ok2 && per > total {
			resp.Diagnostics.AddAttributeError(path.Root("send_limits").AtName("queue_per_chat"), "Per-chat queue larger than the total",
				fmt.Sprintf("queue_per_chat (%d) must not exceed queue_total (%d); a value left out is SpaceMan's stored value or default "+
					"(%d per chat, %d in total).", per, total, tgDefQueuePerChat, tgDefQueueTotal))
		}
	}
	if start, ok1 := str(keyQuietHoursStart, tgDefQuietStart); ok1 {
		if end, ok2 := str(keyQuietHoursEnd, tgDefQuietEnd); ok2 && start == end {
			resp.Diagnostics.AddAttributeError(path.Root("notification_defaults").AtName("quiet_hours_end"), "Empty quiet hours",
				fmt.Sprintf("quiet_hours_end must differ from quiet_hours_start (%s); a value left out is SpaceMan's stored value or default "+
					"(%s to %s).", start, tgDefQuietStart, tgDefQuietEnd))
		}
	}
}
