package opsinventory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ogen-go/ogen/ogenerrors"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	operationGet  = "get"
	operationList = "list"

	getOperationID  = "v1_getInternalResource"
	listOperationID = "v1_listInternalResources"
)

// Handler serves POST /v1/internal/projects/{projectNumber}/locations/{locationId}/resources:get and :list.
type Handler struct {
	store         readstore.ReadStore
	readers       map[ResourceType]Reader
	isOCI         func() bool
	checkLocation func(locationID string) error
	now           func() time.Time
	logger        func(ctx context.Context) log.Logger
}

// Option configures a Handler.
type Option func(*Handler)

// WithCloudCheck replaces the OCI check. Used in tests.
func WithCloudCheck(isOCI func() bool) Option {
	return func(h *Handler) { h.isOCI = isOCI }
}

// WithLocationCheck replaces the location check. Used in tests.
func WithLocationCheck(check func(locationID string) error) Option {
	return func(h *Handler) { h.checkLocation = check }
}

// WithLogger replaces the audit logger. Used in tests.
func WithLogger(logger func(ctx context.Context) log.Logger) Option {
	return func(h *Handler) { h.logger = logger }
}

// NewHandler returns a Handler that serves the given readers.
func NewHandler(store readstore.ReadStore, readers []Reader, opts ...Option) *Handler {
	h := &Handler{
		store:         store,
		readers:       make(map[ResourceType]Reader, len(readers)),
		isOCI:         func() bool { return env.GetHyperscaler() == commonparams.ProviderOCI },
		checkLocation: validateLocation,
		now:           time.Now,
		logger:        func(ctx context.Context) log.Logger { return util.GetLogger(ctx) },
	}
	for _, r := range readers {
		h.readers[r.Spec().Type] = r
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// anyValue in a path segment means "any project" or "any location" (AIP-159).
const anyValue = "-"

// pathScope is the project and location of the request path. Empty means "-".
type pathScope struct {
	projectNumber string
	location      string
}

func newPathScope(projectNumber, locationID string) pathScope {
	scope := pathScope{projectNumber: projectNumber, location: locationID}
	if scope.projectNumber == anyValue {
		scope.projectNumber = ""
	}
	if scope.location == anyValue {
		scope.location = ""
	}
	return scope
}

// validateLocation applies the Google Proxy rule: the location is a region or
// zone, and its region must be this Core's region. A zone does not filter rows.
func validateLocation(locationID string) error {
	if _, _, gcpErr := utils.ParseAndValidateRegionAndZone(locationID); gcpErr != nil {
		return errors.New(gcpErr.Message)
	}
	return nil
}

// checkScope validates the location of the request path.
func (h *Handler) checkScope(scope pathScope) *apiError {
	if scope.location == "" {
		return nil
	}
	if err := h.checkLocation(scope.location); err != nil {
		return badRequest("invalid locationId %q: %s", scope.location, err.Error())
	}
	return nil
}

// apiError is a client-visible failure with its HTTP status. cause is only
// written to the audit log, never to the response.
type apiError struct {
	status  int
	message string
	cause   error
}

func (e *apiError) Error() string {
	if e.cause != nil {
		return e.message + ": " + e.cause.Error()
	}
	return e.message
}

func badRequest(format string, args ...any) *apiError {
	return &apiError{status: http.StatusBadRequest, message: fmt.Sprintf(format, args...)}
}

func notFound(format string, args ...any) *apiError {
	return &apiError{status: http.StatusNotFound, message: fmt.Sprintf(format, args...)}
}

func internalError(cause error) *apiError {
	return &apiError{status: http.StatusInternalServerError, message: "internal error", cause: cause}
}

// fromStoreError maps a read-store or reader error to an apiError.
func fromStoreError(err error, what string) *apiError {
	var apiErr *apiError
	switch {
	case errors.As(err, &apiErr):
		return apiErr
	case errors.Is(err, readstore.ErrNotFound):
		return notFound("%s not found", what)
	case errors.Is(err, readstore.ErrAmbiguousName):
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("ambiguous %s name; use uuid", what)}
	case errors.Is(err, readstore.ErrInvalidQuery):
		return badRequest("invalid query")
	default:
		return internalError(err)
	}
}

// Get handles POST /v1/internal/projects/{projectNumber}/locations/{locationId}/resources:get.
func (h *Handler) Get(ctx context.Context, req *oasgenserver.OpsGetResourceRequestV1, params oasgenserver.V1GetInternalResourceParams) (oasgenserver.V1GetInternalResourceRes, error) {
	rec := h.newAudit(ctx, operationGet, params.XCorrelationID)
	rec.location = params.LocationId
	res, apiErr := h.get(ctx, req, newPathScope(params.ProjectNumber, params.LocationId), rec)
	if apiErr != nil {
		h.finish(ctx, rec, apiErr.status, apiErr)
		return getErrorResponse(apiErr), nil
	}
	h.finish(ctx, rec, http.StatusOK, nil)
	return &res.Value, nil
}

func (h *Handler) get(ctx context.Context, req *oasgenserver.OpsGetResourceRequestV1, scope pathScope, rec *audit) (Resource, *apiError) {
	if req == nil {
		return Resource{}, badRequest("request body is required")
	}
	rec.resourceType = string(req.ResourceType)
	rec.projectNumber = scope.projectNumber
	uuid, name := req.Identity.UUID.Or(""), req.Identity.Name.Or("")
	rec.resourceID = uuid + name
	for _, p := range req.Parents {
		rec.parentFilters = append(rec.parentFilters, string(p.Type)+":"+p.ID)
	}

	if h.isOCI() {
		return Resource{}, &apiError{status: http.StatusNotImplemented, message: "ops inventory API is GCP only"}
	}
	if apiErr := h.checkScope(scope); apiErr != nil {
		return Resource{}, apiErr
	}
	reader, ok := h.readers[req.ResourceType]
	if !ok {
		return Resource{}, badRequest("unsupported resourceType %q", req.ResourceType)
	}
	spec := reader.Spec()
	rec.dataSource = string(oasgenserver.OpsSourceV1VcpDb)

	if (uuid == "") == (name == "") {
		return Resource{}, badRequest("identity needs exactly one of uuid or name")
	}

	if uuid != "" && len(req.Parents) > 0 {
		return Resource{}, badRequest("parents are only used with identity.name")
	}
	if name != "" {
		if !spec.GetByName {
			return Resource{}, badRequest("get by name is not supported for %s; use uuid", spec.Type)
		}
		if scope.projectNumber == "" {
			return Resource{}, badRequest("a project number is required for get by name")
		}
		if apiErr := checkNameParents(spec, req.Parents); apiErr != nil {
			return Resource{}, apiErr
		}
	}

	account, apiErr := h.resolveProject(ctx, scope.projectNumber)
	if apiErr != nil {
		return Resource{}, apiErr
	}

	getReq := GetRequest{UUID: uuid, Name: name, Account: account}
	if name != "" {
		scope, apiErr := h.nameScope(ctx, spec, req.Parents, account)
		if apiErr != nil {
			return Resource{}, apiErr
		}
		getReq.NameScope = scope
	}

	res, err := reader.Get(ctx, getReq)
	if err != nil {
		return Resource{}, fromStoreError(err, string(spec.Type))
	}
	if account != nil && (res.OwnerAccountID == nil || *res.OwnerAccountID != account.ID) {
		return Resource{}, notFound("%s not found", spec.Type)
	}
	if res.EnrichmentStatus != "" {
		rec.enrichmentStatus = res.EnrichmentStatus
	}
	if spec.Type == ResourceTypeJob && res.Value.OpsJobV1.Source == oasgenserver.OpsSourceV1VcpDbAndTemporal {
		rec.dataSource = string(oasgenserver.OpsSourceV1VcpDbAndTemporal)
	}
	return res, nil
}

// checkNameParents requires exactly the parents the type needs for a get by name.
func checkNameParents(spec Spec, parents []oasgenserver.OpsParentV1) *apiError {
	required := map[ParentType]bool{}
	for _, p := range spec.NameParents {
		required[p] = true
	}
	seen := map[ParentType]bool{}
	for _, p := range parents {
		if !required[p.Type] {
			return badRequest("parent %s is not used for get by name of %s", p.Type, spec.Type)
		}
		if seen[p.Type] {
			return badRequest("parent %s is sent more than once", p.Type)
		}
		seen[p.Type] = true
	}
	for _, p := range spec.NameParents {
		if !seen[p] {
			return badRequest("get by name of %s needs parent %s", spec.Type, p)
		}
	}
	return nil
}

// nameScope builds the project and parent predicates of a get by name.
func (h *Handler) nameScope(ctx context.Context, spec Spec, parents []oasgenserver.OpsParentV1, account *datamodel.Account) ([]readstore.Eq, *apiError) {
	var scope []readstore.Eq
	if spec.AccountColumn != "" {
		scope = append(scope, readstore.Eq{Column: spec.AccountColumn, Value: account.ID})
	}
	parentEq, apiErr := h.resolveParents(ctx, spec, parents, account)
	if apiErr != nil {
		return nil, apiErr
	}
	return append(scope, parentEq...), nil
}

// resolveParents turns parents into predicates. Parents with an owner table
// are resolved Unscoped (deleted parents still match) and, where required,
// must belong to the request project; unknown or foreign parents are 404.
func (h *Handler) resolveParents(ctx context.Context, spec Spec, parents []oasgenserver.OpsParentV1, account *datamodel.Account) ([]readstore.Eq, *apiError) {
	eqs := make([]readstore.Eq, 0, len(parents))
	for _, p := range parents {
		ps := spec.ListParents[p.Type]
		if ps.Owner == "" {
			eqs = append(eqs, readstore.Eq{Column: ps.Column, Value: p.ID})
			continue
		}
		owner, err := h.store.LookupOwner(ctx, ps.Owner, p.ID)
		if errors.Is(err, readstore.ErrNotFound) {
			return nil, notFound("parent %s %s not found", p.Type, p.ID)
		}
		if err != nil {
			return nil, internalError(err)
		}
		if ps.CheckOwner && (account == nil || owner.AccountID == nil || *owner.AccountID != account.ID) {
			return nil, notFound("parent %s %s not found", p.Type, p.ID)
		}
		if ps.ByID {
			eqs = append(eqs, readstore.Eq{Column: ps.Column, Value: owner.ID})
		} else {
			eqs = append(eqs, readstore.Eq{Column: ps.Column, Value: p.ID})
		}
	}
	return eqs, nil
}

func (h *Handler) resolveProject(ctx context.Context, projectNumber string) (*datamodel.Account, *apiError) {
	if projectNumber == "" {
		return nil, nil
	}
	account, err := h.store.GetAccountByName(ctx, projectNumber)
	if errors.Is(err, readstore.ErrNotFound) {
		return nil, notFound("project not found")
	}
	if errors.Is(err, readstore.ErrAmbiguousName) {
		return nil, &apiError{status: http.StatusConflict, message: "more than one live project matches projectNumber"}
	}
	if err != nil {
		return nil, internalError(err)
	}
	return account, nil
}

// List handles POST /v1/internal/projects/{projectNumber}/locations/{locationId}/resources:list.
func (h *Handler) List(ctx context.Context, req *oasgenserver.OpsListResourcesRequestV1, params oasgenserver.V1ListInternalResourcesParams) (oasgenserver.V1ListInternalResourcesRes, error) {
	rec := h.newAudit(ctx, operationList, params.XCorrelationID)
	rec.location = params.LocationId
	resp, apiErr := h.list(ctx, req, newPathScope(params.ProjectNumber, params.LocationId), rec)
	if apiErr != nil {
		h.finish(ctx, rec, apiErr.status, apiErr)
		return listErrorResponse(apiErr), nil
	}
	h.finish(ctx, rec, http.StatusOK, nil)
	return resp, nil
}

func (h *Handler) list(ctx context.Context, req *oasgenserver.OpsListResourcesRequestV1, scope pathScope, rec *audit) (*oasgenserver.OpsListResourcesResponseV1, *apiError) {
	if req == nil {
		return nil, badRequest("request body is required")
	}
	key := h.recordList(req, scope, rec)

	if h.isOCI() {
		return nil, &apiError{status: http.StatusNotImplemented, message: "ops inventory API is GCP only"}
	}
	if apiErr := h.checkScope(scope); apiErr != nil {
		return nil, apiErr
	}
	reader, ok := h.readers[req.ResourceType]
	if !ok {
		return nil, badRequest("unsupported resourceType %q", req.ResourceType)
	}
	spec := reader.Spec()
	rec.dataSource = string(oasgenserver.OpsSourceV1VcpDb)

	pageSize := int(req.PageSize.Or(0))
	switch {
	case pageSize < 0:
		return nil, badRequest("pageSize must not be negative")
	case pageSize == 0:
		pageSize = defaultPageSize
	case pageSize > maxPageSize:
		pageSize = maxPageSize
	}
	rec.pageSize = &pageSize

	opts := readstore.ListOptions{IncludeDeleted: req.IncludeDeleted.Or(false), Limit: pageSize + 1}
	hasOtherFilter := false

	// Parents.
	seenParents := map[ParentType]bool{}
	for _, p := range req.Parents {
		if _, ok := spec.ListParents[p.Type]; !ok {
			return nil, badRequest("parent %s is not supported for %s", p.Type, spec.Type)
		}
		if seenParents[p.Type] {
			return nil, badRequest("parent %s is sent more than once", p.Type)
		}
		if p.ID == "" {
			return nil, badRequest("parent %s needs an id", p.Type)
		}
		seenParents[p.Type] = true
		hasOtherFilter = true
	}
	for _, p := range spec.RequiredListParents {
		if !seenParents[p] {
			return nil, badRequest("list of %s needs parent %s", spec.Type, p)
		}
	}

	// Time filters.
	seenTimes := map[TimeField]bool{}
	for _, tf := range req.TimeFilters {
		column, ok := spec.TimeFields[tf.Field]
		if !ok {
			return nil, badRequest("time filter %s is not supported for %s", tf.Field, spec.Type)
		}
		if seenTimes[tf.Field] {
			return nil, badRequest("time filter %s is sent more than once", tf.Field)
		}
		seenTimes[tf.Field] = true
		tr := readstore.TimeRange{Column: column}
		if from, ok := tf.From.Get(); ok {
			from = from.UTC()
			tr.From = &from
		}
		if to, ok := tf.To.Get(); ok {
			to = to.UTC()
			tr.To = &to
		}
		if tr.From == nil && tr.To == nil {
			return nil, badRequest("time filter %s needs from or to", tf.Field)
		}
		if tr.From != nil && tr.To != nil && !tr.From.Before(*tr.To) {
			return nil, badRequest("time filter %s needs from before to", tf.Field)
		}
		opts.TimeRanges = append(opts.TimeRanges, tr)
		hasOtherFilter = true
	}

	// Field filters.
	if filters, ok := req.Filters.Get(); ok {
		if name, ok := filters.Name.Get(); ok {
			if !spec.NameFilter {
				return nil, badRequest("filter name is not supported for %s", spec.Type)
			}
			opts.Eq = append(opts.Eq, readstore.Eq{Column: readstore.ColName, Value: name})
		}
		if state, ok := filters.State.Get(); ok {
			if _, valid := spec.States[state]; !valid {
				return nil, badRequest("unknown state %q for %s", state, spec.Type)
			}
			opts.Eq = append(opts.Eq, readstore.Eq{Column: spec.StateColumn, Value: state})
			hasOtherFilter = true
		}
		for filterKey, value := range map[string]oasgenserver.OptString{
			FilterCorrelationID: filters.CorrelationId,
			FilterType:          filters.Type,
		} {
			v, ok := value.Get()
			if !ok {
				continue
			}
			column, supported := spec.ExtraFilters[filterKey]
			if !supported {
				return nil, badRequest("filter %s is not supported for %s", filterKey, spec.Type)
			}
			opts.Eq = append(opts.Eq, readstore.Eq{Column: column, Value: v})
			hasOtherFilter = true
		}
	}

	// Project.
	if scope.projectNumber == "" {
		if !spec.ProjectOptional {
			return nil, badRequest("a project number is required for list of %s", spec.Type)
		}
		if !hasOtherFilter {
			return nil, badRequest("list of %s across projects needs another filter", spec.Type)
		}
	}

	// Page token.
	if token, ok := req.PageToken.Get(); ok && token != "" {
		afterID, err := decodePageToken(token, key.hash())
		if err != nil {
			return nil, badRequest("%s", err.Error())
		}
		opts.AfterID = afterID
	}

	account, apiErr := h.resolveProject(ctx, scope.projectNumber)
	if apiErr != nil {
		return nil, apiErr
	}
	if account != nil && spec.AccountColumn != "" {
		opts.Eq = append(opts.Eq, readstore.Eq{Column: spec.AccountColumn, Value: account.ID})
	}
	parentEq, apiErr := h.resolveParents(ctx, spec, req.Parents, account)
	if apiErr != nil {
		return nil, apiErr
	}
	opts.Eq = append(opts.Eq, parentEq...)

	rows, err := reader.List(ctx, ListRequest{Account: account, Options: opts})
	if err != nil {
		return nil, fromStoreError(err, string(spec.Type))
	}

	resp := &oasgenserver.OpsListResourcesResponseV1{
		Resources:     make([]oasgenserver.OpsResourceV1, 0, min(len(rows), pageSize)),
		NextPageToken: oasgenserver.NewOptString(""),
	}
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		resp.NextPageToken = oasgenserver.NewOptString(encodePageToken(rows[len(rows)-1].ID, key.hash()))
	}
	for _, row := range rows {
		resp.Resources = append(resp.Resources, row.Value)
	}
	count := len(resp.Resources)
	rec.resultCount = &count
	if spec.Type == ResourceTypeJob {
		rec.enrichmentStatus = string(oasgenserver.OpsJobTemporalV1EnrichmentStatusSKIPPED)
	}
	return resp, nil
}

// recordList copies the request filters into the audit record and returns
// the normalized key the page token is bound to.
func (h *Handler) recordList(req *oasgenserver.OpsListResourcesRequestV1, scope pathScope, rec *audit) listFilterKey {
	key := listFilterKey{
		resourceType:   string(req.ResourceType),
		projectNumber:  scope.projectNumber,
		fieldFilters:   map[string]string{},
		includeDeleted: req.IncludeDeleted.Or(false),
	}
	for _, p := range req.Parents {
		key.parents = append(key.parents, string(p.Type)+":"+p.ID)
	}
	for _, tf := range req.TimeFilters {
		var from, to *time.Time
		if v, ok := tf.From.Get(); ok {
			from = &v
		}
		if v, ok := tf.To.Get(); ok {
			to = &v
		}
		key.timeFilters = append(key.timeFilters, formatTimeFilter(string(tf.Field), from, to))
	}
	if filters, ok := req.Filters.Get(); ok {
		for k, v := range map[string]oasgenserver.OptString{
			"name":              filters.Name,
			"state":             filters.State,
			FilterCorrelationID: filters.CorrelationId,
			FilterType:          filters.Type,
		} {
			if s, ok := v.Get(); ok {
				key.fieldFilters[k] = s
			}
		}
	}

	rec.resourceType = key.resourceType
	rec.projectNumber = key.projectNumber
	rec.parentFilters = key.parents
	rec.timeFilters = key.timeFilters
	rec.fieldFilters = key.fieldFilters
	includeDeleted := key.includeDeleted
	rec.includeDeleted = &includeDeleted
	return key
}

func getErrorResponse(e *apiError) oasgenserver.V1GetInternalResourceRes {
	body := oasgenserver.Error{Code: float64(e.status), Message: e.message}
	switch e.status {
	case http.StatusBadRequest:
		r := oasgenserver.V1GetInternalResourceBadRequest(body)
		return &r
	case http.StatusNotFound:
		r := oasgenserver.V1GetInternalResourceNotFound(body)
		return &r
	case http.StatusConflict:
		r := oasgenserver.V1GetInternalResourceConflict(body)
		return &r
	case http.StatusNotImplemented:
		r := oasgenserver.V1GetInternalResourceNotImplemented(body)
		return &r
	default:
		r := oasgenserver.V1GetInternalResourceInternalServerError(body)
		return &r
	}
}

func listErrorResponse(e *apiError) oasgenserver.V1ListInternalResourcesRes {
	body := oasgenserver.Error{Code: float64(e.status), Message: e.message}
	switch e.status {
	case http.StatusBadRequest:
		r := oasgenserver.V1ListInternalResourcesBadRequest(body)
		return &r
	case http.StatusNotFound:
		r := oasgenserver.V1ListInternalResourcesNotFound(body)
		return &r
	case http.StatusNotImplemented:
		r := oasgenserver.V1ListInternalResourcesNotImplemented(body)
		return &r
	default:
		r := oasgenserver.V1ListInternalResourcesInternalServerError(body)
		return &r
	}
}

// ErrorHandler wraps the ogen error handler. For the two ops inventory
// operations it audits requests rejected by the generated decoder (unknown
// fields, bad enum, path pattern) and answers with the API Error schema
// {code, message} instead of ogen's {"error_message": ...}, so clients see one
// error shape and no generated type names. Other routes go to next unchanged.
func (h *Handler) ErrorHandler(next ogenerrors.ErrorHandler) ogenerrors.ErrorHandler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
		op := ""
		var opErr interface{ OperationID() string }
		if errors.As(err, &opErr) {
			switch opErr.OperationID() {
			case getOperationID:
				op = operationGet
			case listOperationID:
				op = operationList
			}
		}
		if op == "" {
			next(ctx, w, r, err)
			return
		}

		status := ogenerrors.ErrorCode(err)
		correlationID := oasgenserver.OptString{}
		if id := r.Header.Get(log.RequestCorrelationID); id != "" {
			correlationID = oasgenserver.NewOptString(id)
		}
		rec := h.newAudit(ctx, op, correlationID)
		h.finish(ctx, rec, status, errors.New("request rejected by schema validation"))

		message := "internal error"
		if status < http.StatusInternalServerError {
			message = rejectionMessage(err)
		}
		body, _ := (&oasgenserver.Error{Code: float64(status), Message: message}).MarshalJSON()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

// decoderNoise matches the segments ogen adds while it decodes a request:
// operation and step names and generated type names. They say nothing to an
// API caller and are dropped from rejection messages.
var decoderNoise = regexp.MustCompile(`^(operation \w+|decode (request|params|application/json)|callback|validate|invalid|decode (Opt)?Ops\w*V1\w*)$`)

// rejectionMessage turns an ogen decode error into a short message, for
// example `invalid request: field "identity": unexpected field "id"` or
// `invalid request: pageSize (int: value -1 less than 0)`.
func rejectionMessage(err error) string {
	var kept []string
	for _, segment := range strings.Split(err.Error(), ": ") {
		if decoderNoise.MatchString(segment) {
			continue
		}
		kept = append(kept, strings.TrimPrefix(segment, "decode "))
	}
	if len(kept) == 0 {
		return "invalid request"
	}
	return "invalid request: " + strings.Join(kept, ": ")
}

// audit is the one structured completion log per call. It never holds
// response bodies, credentials or resource payloads.
type audit struct {
	start            time.Time
	correlationID    string
	operation        string
	resourceType     string
	resourceID       string
	projectNumber    string
	location         string
	parentFilters    []string
	timeFilters      []string
	fieldFilters     map[string]string
	includeDeleted   *bool
	pageSize         *int
	resultCount      *int
	dataSource       string
	enrichmentStatus string
}

func (h *Handler) newAudit(ctx context.Context, operation string, correlationID oasgenserver.OptString) *audit {
	id := correlationID.Or("")
	if id == "" {
		id = utils.GetCoRelationIDFromContext(ctx)
	}
	return &audit{start: h.now(), correlationID: id, operation: operation}
}

func auditResult(status int) string {
	switch {
	case status < 300:
		return "success"
	case status == http.StatusNotFound:
		return "not_found"
	case status == http.StatusConflict:
		return "conflict"
	case status == http.StatusNotImplemented:
		return "not_implemented"
	case status < 500:
		return "bad_request"
	default:
		return "error"
	}
}

func (h *Handler) finish(ctx context.Context, rec *audit, status int, cause error) {
	fields := log.Fields{
		"correlation_id": rec.correlationID,
		"operation":      rec.operation,
		"resource_type":  rec.resourceType,
		"resource_id":    rec.resourceID,
		"project_number": rec.projectNumber,
		"location":       rec.location,
		"parent_filters": rec.parentFilters,
		"time_filters":   rec.timeFilters,
		"field_filters":  rec.fieldFilters,
		"data_source":    rec.dataSource,
		"result":         auditResult(status),
		"http_status":    status,
		"duration_ms":    h.now().Sub(rec.start).Milliseconds(),
	}
	if rec.includeDeleted != nil {
		fields["include_deleted"] = *rec.includeDeleted
	}
	if rec.pageSize != nil {
		fields["page_size"] = *rec.pageSize
	}
	if rec.resultCount != nil {
		fields["result_count"] = *rec.resultCount
	}
	if rec.enrichmentStatus != "" {
		fields["enrichment_status"] = rec.enrichmentStatus
	}
	if cause != nil {
		fields["reason"] = cause.Error()
	}
	h.logger(ctx).With(fields).Info("ops-inventory: read completed")
}
