package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Per-type params, mirroring quickwit-config/src/source_config/mod.rs.

func backfillAttr() schema.BoolAttribute {
	return schema.BoolAttribute{
		Optional: true, Computed: true, Default: booldefault.StaticBool(false),
		MarkdownDescription: "Backfill mode: index the existing messages, then stop (the source is finished once it reaches the end). Defaults to `false`.",
	}
}

func immutableString(required bool, desc string) schema.StringAttribute {
	return schema.StringAttribute{
		Required: required, Optional: !required, MarkdownDescription: desc + " Changing it recreates the source.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func decodeParams(raw json.RawMessage, dst any) diag.Diagnostics {
	var diags diag.Diagnostics
	if err := json.Unmarshal(raw, dst); err != nil {
		diags.AddError("Unexpected source params from Quickwit", err.Error())
	}
	return diags
}

// ---------------------------------------------------------------------------
// Kafka
// ---------------------------------------------------------------------------

type kafkaModel struct {
	SourceCommon
	Topic              types.String `tfsdk:"topic"`
	ClientLogLevel     types.String `tfsdk:"client_log_level"`
	ClientParams       types.Map    `tfsdk:"client_params"`
	EnableBackfillMode types.Bool   `tfsdk:"enable_backfill_mode"`
}

var kafkaSpec = sourceSpec{
	name: "kafka", sourceType: "kafka", multiplePipelines: true,
	desc: "A Kafka source: Quickwit consumes a topic, with exactly-once semantics through its own checkpoints.",
	attrs: map[string]schema.Attribute{
		"topic": immutableString(true, "Topic to consume."),
		"client_log_level": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "librdkafka log level: `debug`, `info`, `warn` or `error`.",
			Validators:          []validator.String{stringvalidator.OneOf("debug", "info", "warn", "error")},
		},
		"client_params": schema.MapAttribute{
			Optional: true, Sensitive: true, ElementType: types.StringType,
			MarkdownDescription: "[librdkafka configuration](https://github.com/confluentinc/librdkafka/blob/master/CONFIGURATION.md) (`bootstrap.servers`, `security.protocol`, `sasl.password`…). Sensitive.",
		},
		"enable_backfill_mode": backfillAttr(),
	},
}

func (m *kafkaModel) common() *SourceCommon { return &m.SourceCommon }

func (m *kafkaModel) params(ctx context.Context) (any, diag.Diagnostics) {
	var diags diag.Diagnostics
	p := map[string]any{"topic": m.Topic.ValueString(), "enable_backfill_mode": m.EnableBackfillMode.ValueBool()}
	if !m.ClientLogLevel.IsNull() {
		p["client_log_level"] = m.ClientLogLevel.ValueString()
	}
	if !m.ClientParams.IsNull() {
		cp := map[string]string{}
		diags.Append(m.ClientParams.ElementsAs(ctx, &cp, false)...)
		p["client_params"] = cp
	}
	return p, diags
}

func (m *kafkaModel) setParams(ctx context.Context, raw json.RawMessage) diag.Diagnostics {
	var p struct {
		Topic              string         `json:"topic"`
		ClientLogLevel     *string        `json:"client_log_level"`
		ClientParams       map[string]any `json:"client_params"`
		EnableBackfillMode bool           `json:"enable_backfill_mode"`
	}
	diags := decodeParams(raw, &p)
	m.Topic = types.StringValue(p.Topic)
	m.ClientLogLevel = types.StringPointerValue(p.ClientLogLevel)
	m.EnableBackfillMode = types.BoolValue(p.EnableBackfillMode)
	m.ClientParams = types.MapNull(types.StringType)
	if p.ClientParams != nil {
		// Params set outside Terraform may be numbers or booleans; librdkafka
		// reads them all as strings anyway.
		cp := make(map[string]string, len(p.ClientParams))
		for k, v := range p.ClientParams {
			if s, ok := v.(string); ok {
				cp[k] = s
			} else {
				b, _ := json.Marshal(v)
				cp[k] = string(b)
			}
		}
		var d diag.Diagnostics
		m.ClientParams, d = types.MapValueFrom(ctx, types.StringType, cp)
		diags.Append(d...)
	}
	return diags
}

// ---------------------------------------------------------------------------
// Kinesis
// ---------------------------------------------------------------------------

type kinesisModel struct {
	SourceCommon
	StreamName         types.String `tfsdk:"stream_name"`
	Region             types.String `tfsdk:"region"`
	Endpoint           types.String `tfsdk:"endpoint"`
	EnableBackfillMode types.Bool   `tfsdk:"enable_backfill_mode"`
}

var kinesisSpec = sourceSpec{
	name: "kinesis", sourceType: "kinesis",
	desc: "An Amazon Kinesis Data Streams source. AWS credentials come from the Quickwit node's environment.",
	attrs: map[string]schema.Attribute{
		"stream_name": immutableString(true, "Name of the stream."),
		"region": func() schema.StringAttribute {
			a := immutableString(false, "AWS region of the stream. Defaults to the node's region. Conflicts with `endpoint`.")
			a.Validators = []validator.String{stringvalidator.ConflictsWith(path.MatchRoot("endpoint"))}
			return a
		}(),
		"endpoint":             immutableString(false, "Custom Kinesis endpoint (e.g. LocalStack). Conflicts with `region`."),
		"enable_backfill_mode": backfillAttr(),
	},
}

func (m *kinesisModel) common() *SourceCommon { return &m.SourceCommon }

func (m *kinesisModel) params(context.Context) (any, diag.Diagnostics) {
	p := map[string]any{"stream_name": m.StreamName.ValueString(), "enable_backfill_mode": m.EnableBackfillMode.ValueBool()}
	if !m.Region.IsNull() {
		p["region"] = m.Region.ValueString()
	}
	if !m.Endpoint.IsNull() {
		p["endpoint"] = m.Endpoint.ValueString()
	}
	return p, nil
}

func (m *kinesisModel) setParams(_ context.Context, raw json.RawMessage) diag.Diagnostics {
	var p struct {
		StreamName         string  `json:"stream_name"`
		Region             *string `json:"region"`
		Endpoint           *string `json:"endpoint"`
		EnableBackfillMode bool    `json:"enable_backfill_mode"`
	}
	diags := decodeParams(raw, &p)
	m.StreamName = types.StringValue(p.StreamName)
	m.Region = types.StringPointerValue(p.Region)
	m.Endpoint = types.StringPointerValue(p.Endpoint)
	m.EnableBackfillMode = types.BoolValue(p.EnableBackfillMode)
	return diags
}

// ---------------------------------------------------------------------------
// Pulsar
// ---------------------------------------------------------------------------

type pulsarModel struct {
	SourceCommon
	Topics         types.List   `tfsdk:"topics"`
	Address        types.String `tfsdk:"address"`
	ConsumerName   types.String `tfsdk:"consumer_name"`
	Authentication types.Object `tfsdk:"authentication"`
}

type pulsarAuthModel struct {
	Token  types.String `tfsdk:"token"`
	OAuth2 types.Object `tfsdk:"oauth2"`
}

type pulsarOAuth2Model struct {
	IssuerURL      types.String `tfsdk:"issuer_url"`
	CredentialsURL types.String `tfsdk:"credentials_url"`
	Audience       types.String `tfsdk:"audience"`
	Scope          types.String `tfsdk:"scope"`
}

type apiPulsarOAuth2 struct {
	IssuerURL      string  `json:"issuer_url"`
	CredentialsURL string  `json:"credentials_url"`
	Audience       *string `json:"audience"`
	Scope          *string `json:"scope"`
}

var (
	pulsarOAuth2AttrTypes = map[string]attr.Type{
		"issuer_url": types.StringType, "credentials_url": types.StringType, "audience": types.StringType, "scope": types.StringType,
	}
	pulsarAuthAttrTypes = map[string]attr.Type{"token": types.StringType, "oauth2": types.ObjectType{AttrTypes: pulsarOAuth2AttrTypes}}
	pulsarAddressRegex  = regexp.MustCompile(`^pulsar(\+ssl)?://`)
)

var pulsarSpec = sourceSpec{
	name: "pulsar", sourceType: "pulsar",
	desc: "An Apache Pulsar source.",
	attrs: map[string]schema.Attribute{
		"topics": schema.ListAttribute{
			Required: true, ElementType: types.StringType, MarkdownDescription: "Topics to consume.",
			Validators: []validator.List{listvalidator.SizeAtLeast(1)},
		},
		"address": schema.StringAttribute{
			Required: true, MarkdownDescription: "Broker URL, `pulsar://host:port` or `pulsar+ssl://host:port`.",
			Validators: []validator.String{stringvalidator.RegexMatches(pulsarAddressRegex, "must be in the format of `pulsar://host:port/path`")},
		},
		"consumer_name": schema.StringAttribute{
			Optional: true, Computed: true, Default: stringdefault.StaticString("quickwit"),
			MarkdownDescription: "Consumer name. Defaults to `quickwit`.",
		},
		"authentication": schema.SingleNestedAttribute{
			Optional: true, Sensitive: true,
			MarkdownDescription: "Authentication: set exactly one of `token` or `oauth2`. Sensitive.",
			Validators:          []validator.Object{pulsarAuthValidator{}},
			Attributes: map[string]schema.Attribute{
				"token": schema.StringAttribute{
					Optional: true, Sensitive: true, MarkdownDescription: "JWT token.",
					Validators: []validator.String{stringvalidator.ConflictsWith(path.MatchRoot("authentication").AtName("oauth2"))},
				},
				"oauth2": schema.SingleNestedAttribute{
					Optional: true, MarkdownDescription: "OAuth2 client credentials flow.",
					Attributes: map[string]schema.Attribute{
						"issuer_url":      schema.StringAttribute{Required: true, MarkdownDescription: "Issuer URL."},
						"credentials_url": schema.StringAttribute{Required: true, MarkdownDescription: "URL of the credentials file (`file:///path/creds.json` or `data:…`)."},
						"audience":        schema.StringAttribute{Optional: true, MarkdownDescription: "Audience."},
						"scope":           schema.StringAttribute{Optional: true, MarkdownDescription: "Scope."},
					},
				},
			},
		},
	},
}

func (m *pulsarModel) common() *SourceCommon { return &m.SourceCommon }

func (m *pulsarModel) params(ctx context.Context) (any, diag.Diagnostics) {
	var diags diag.Diagnostics
	topics := []string{}
	diags.Append(m.Topics.ElementsAs(ctx, &topics, false)...)
	p := map[string]any{"topics": topics, "address": m.Address.ValueString(), "consumer_name": m.ConsumerName.ValueString()}
	if !m.Authentication.IsNull() {
		var a pulsarAuthModel
		diags.Append(m.Authentication.As(ctx, &a, basetypes.ObjectAsOptions{})...)
		switch {
		case !a.Token.IsNull():
			p["authentication"] = map[string]any{"token": a.Token.ValueString()}
		case !a.OAuth2.IsNull():
			var o pulsarOAuth2Model
			diags.Append(a.OAuth2.As(ctx, &o, basetypes.ObjectAsOptions{})...)
			p["authentication"] = map[string]any{"oauth2": apiPulsarOAuth2{
				IssuerURL: o.IssuerURL.ValueString(), CredentialsURL: o.CredentialsURL.ValueString(),
				Audience: o.Audience.ValueStringPointer(), Scope: o.Scope.ValueStringPointer(),
			}}
		}
	}
	return p, diags
}

func (m *pulsarModel) setParams(ctx context.Context, raw json.RawMessage) diag.Diagnostics {
	var p struct {
		Topics         []string `json:"topics"`
		Address        string   `json:"address"`
		ConsumerName   string   `json:"consumer_name"`
		Authentication *struct {
			Token  *string          `json:"token"`
			OAuth2 *apiPulsarOAuth2 `json:"oauth2"`
		} `json:"authentication"`
	}
	diags := decodeParams(raw, &p)
	var d diag.Diagnostics
	m.Topics, d = types.ListValueFrom(ctx, types.StringType, p.Topics)
	diags.Append(d...)
	m.Address = types.StringValue(p.Address)
	m.ConsumerName = types.StringValue(p.ConsumerName)
	m.Authentication = types.ObjectNull(pulsarAuthAttrTypes)
	if a := p.Authentication; a != nil {
		auth := pulsarAuthModel{Token: types.StringPointerValue(a.Token), OAuth2: types.ObjectNull(pulsarOAuth2AttrTypes)}
		if o := a.OAuth2; o != nil {
			auth.OAuth2, d = types.ObjectValueFrom(ctx, pulsarOAuth2AttrTypes, pulsarOAuth2Model{
				IssuerURL: types.StringValue(o.IssuerURL), CredentialsURL: types.StringValue(o.CredentialsURL),
				Audience: types.StringPointerValue(o.Audience), Scope: types.StringPointerValue(o.Scope),
			})
			diags.Append(d...)
		}
		m.Authentication, d = types.ObjectValueFrom(ctx, pulsarAuthAttrTypes, auth)
		diags.Append(d...)
	}
	return diags
}

// ---------------------------------------------------------------------------
// Google Cloud Pub/Sub
// ---------------------------------------------------------------------------

type pubsubModel struct {
	SourceCommon
	Subscription       types.String `tfsdk:"subscription"`
	ProjectID          types.String `tfsdk:"project_id"`
	CredentialsFile    types.String `tfsdk:"credentials_file"`
	MaxMessagesPerPull types.Int64  `tfsdk:"max_messages_per_pull"`
	EnableBackfillMode types.Bool   `tfsdk:"enable_backfill_mode"`
}

var pubsubSpec = sourceSpec{
	name: "pubsub", sourceType: "pubsub", multiplePipelines: true,
	desc: "A Google Cloud Pub/Sub source.",
	attrs: map[string]schema.Attribute{
		"subscription": schema.StringAttribute{Required: true, MarkdownDescription: "Name of the subscription to consume."},
		"project_id":   schema.StringAttribute{Optional: true, MarkdownDescription: "GCP project. Defaults to the project of the credentials."},
		"credentials_file": schema.StringAttribute{
			Optional: true, MarkdownDescription: "Path, on the Quickwit nodes, of a service account key. Defaults to Application Default Credentials.",
		},
		"max_messages_per_pull": schema.Int64Attribute{
			Optional: true, MarkdownDescription: "Maximum number of messages per pull request.",
			Validators: []validator.Int64{int64validator.Between(1, 1<<31-1)},
		},
		"enable_backfill_mode": backfillAttr(),
	},
}

func (m *pubsubModel) common() *SourceCommon { return &m.SourceCommon }

func (m *pubsubModel) params(context.Context) (any, diag.Diagnostics) {
	return map[string]any{
		"subscription":          m.Subscription.ValueString(),
		"project_id":            m.ProjectID.ValueStringPointer(),
		"credentials_file":      m.CredentialsFile.ValueStringPointer(),
		"max_messages_per_pull": m.MaxMessagesPerPull.ValueInt64Pointer(),
		"enable_backfill_mode":  m.EnableBackfillMode.ValueBool(),
	}, nil
}

func (m *pubsubModel) setParams(_ context.Context, raw json.RawMessage) diag.Diagnostics {
	var p struct {
		Subscription       string  `json:"subscription"`
		ProjectID          *string `json:"project_id"`
		CredentialsFile    *string `json:"credentials_file"`
		MaxMessagesPerPull *int64  `json:"max_messages_per_pull"`
		EnableBackfillMode bool    `json:"enable_backfill_mode"`
	}
	diags := decodeParams(raw, &p)
	m.Subscription = types.StringValue(p.Subscription)
	m.ProjectID = types.StringPointerValue(p.ProjectID)
	m.CredentialsFile = types.StringPointerValue(p.CredentialsFile)
	m.MaxMessagesPerPull = types.Int64PointerValue(p.MaxMessagesPerPull)
	m.EnableBackfillMode = types.BoolValue(p.EnableBackfillMode)
	return diags
}

// ---------------------------------------------------------------------------
// File source with SQS notifications
// ---------------------------------------------------------------------------

type sqsModel struct {
	SourceCommon
	QueueURL                         types.String `tfsdk:"queue_url"`
	MessageType                      types.String `tfsdk:"message_type"`
	DeduplicationWindowDurationSecs  types.Int64  `tfsdk:"deduplication_window_duration_secs"`
	DeduplicationWindowMaxMessages   types.Int64  `tfsdk:"deduplication_window_max_messages"`
	DeduplicationCleanupIntervalSecs types.Int64  `tfsdk:"deduplication_cleanup_interval_secs"`
}

type apiSQSNotification struct {
	Type                             string `json:"type"`
	QueueURL                         string `json:"queue_url"`
	MessageType                      string `json:"message_type"`
	DeduplicationWindowDurationSecs  int64  `json:"deduplication_window_duration_secs"`
	DeduplicationWindowMaxMessages   int64  `json:"deduplication_window_max_messages"`
	DeduplicationCleanupIntervalSecs int64  `json:"deduplication_cleanup_interval_secs"`
}

func defU32(def int64, desc string) schema.Int64Attribute {
	return schema.Int64Attribute{
		Optional: true, Computed: true, Default: int64default.StaticInt64(def), MarkdownDescription: desc,
		Validators: []validator.Int64{int64validator.Between(0, 1<<32-1)},
	}
}

var sqsSpec = sourceSpec{
	name: "sqs", sourceType: "file", multiplePipelines: true,
	desc: "A file source fed by an SQS queue: each message points to a file (on S3 or another object store) to index.",
	matches: func(raw json.RawMessage) bool {
		return bytes.Contains(raw, []byte(`"notifications"`))
	},
	attrs: map[string]schema.Attribute{
		"queue_url": schema.StringAttribute{Required: true, MarkdownDescription: "SQS queue URL. Its host is used as the SQS endpoint, so LocalStack URLs work."},
		"message_type": schema.StringAttribute{
			Required: true, Validators: []validator.String{stringvalidator.OneOf("s3_notification", "raw_uri")},
			MarkdownDescription: "Message format: `s3_notification` (S3 event notifications) or `raw_uri` (the message body is the file URI).",
		},
		"deduplication_window_duration_secs":  defU32(3600, "How long, in seconds, processed files are remembered to deduplicate messages. Defaults to `3600`."),
		"deduplication_window_max_messages":   defU32(100_000, "Maximum number of files remembered for deduplication. Defaults to `100000`."),
		"deduplication_cleanup_interval_secs": defU32(60, "Interval, in seconds, between cleanups of the deduplication window. Defaults to `60`."),
	},
}

func (m *sqsModel) common() *SourceCommon { return &m.SourceCommon }

func (m *sqsModel) params(context.Context) (any, diag.Diagnostics) {
	return map[string]any{"notifications": []apiSQSNotification{{
		Type: "sqs", QueueURL: m.QueueURL.ValueString(), MessageType: m.MessageType.ValueString(),
		DeduplicationWindowDurationSecs:  m.DeduplicationWindowDurationSecs.ValueInt64(),
		DeduplicationWindowMaxMessages:   m.DeduplicationWindowMaxMessages.ValueInt64(),
		DeduplicationCleanupIntervalSecs: m.DeduplicationCleanupIntervalSecs.ValueInt64(),
	}}}, nil
}

func (m *sqsModel) setParams(_ context.Context, raw json.RawMessage) diag.Diagnostics {
	var p struct {
		Notifications []apiSQSNotification `json:"notifications"`
	}
	diags := decodeParams(raw, &p)
	if len(p.Notifications) != 1 || p.Notifications[0].Type != "sqs" {
		diags.AddError("Unexpected file source", fmt.Sprintf("expected exactly one SQS notification, got %s", raw))
		return diags
	}
	n := p.Notifications[0]
	m.QueueURL = types.StringValue(n.QueueURL)
	m.MessageType = types.StringValue(n.MessageType)
	m.DeduplicationWindowDurationSecs = types.Int64Value(n.DeduplicationWindowDurationSecs)
	m.DeduplicationWindowMaxMessages = types.Int64Value(n.DeduplicationWindowMaxMessages)
	m.DeduplicationCleanupIntervalSecs = types.Int64Value(n.DeduplicationCleanupIntervalSecs)
	return diags
}

// pulsarAuthValidator requires one of `token` or `oauth2` (ConflictsWith on
// `token` already rejects both).
type pulsarAuthValidator struct{}

func (pulsarAuthValidator) Description(context.Context) string {
	return "one of `token` or `oauth2` must be set"
}

func (v pulsarAuthValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (pulsarAuthValidator) ValidateObject(_ context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	attrs := req.ConfigValue.Attributes()
	if attrs["token"].IsNull() && attrs["oauth2"].IsNull() {
		resp.Diagnostics.AddAttributeError(req.Path, "Missing Pulsar authentication method", "set one of `token` or `oauth2`")
	}
}
