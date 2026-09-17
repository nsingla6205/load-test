package utils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	ontapserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/api/ontap-proxy-servergen"
)

const (
	// OntapAPISegment is the path segment that identifies the start of ONTAP API paths
	OntapAPISegment = "ontap"
	// ontapErrorFallbackMessage is returned to the client when the ONTAP error body cannot be parsed, to avoid leaking raw response (which may be large or sensitive) to API callers.
	ontapErrorFallbackMessage = "ONTAP returned an error"
	// OntapFirstLoginBanner is the message ONTAP prints on first CLI login; we strip it from responses.
	OntapFirstLoginBanner = "This is your first recorded login."
)

var (
	ontapFirstLoginBannerRe           = regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(OntapFirstLoginBanner) + `\s*[\r\n]*`)
	snaplockStatusRe                  = regexp.MustCompile(`(?m)^\s*Status:\s*(.+)$`)
	snaplockOperationTypeRe           = regexp.MustCompile(`(?m)^\s*Operation Type:\s*(.+)$`)
	snaplockNumFilesProcessedRe       = regexp.MustCompile(`(?m)^\s*Number of Files Processed:\s*(.+)$`)
	snaplockNumFilesFailedRe          = regexp.MustCompile(`(?m)^\s*Number of Files Failed:\s*(.+)$`)
	snaplockNumFilesSkippedRe         = regexp.MustCompile(`(?m)^\s*Number of Files Skipped:\s*(.+)$`)
	snaplockNumInodesIgnoredRe        = regexp.MustCompile(`(?m)^\s*Number of Inodes Ignored:\s*(.+)$`)
	snaplockStatusDetailsRe           = regexp.MustCompile(`(?m)^\s*Status Details:\s*(.+)$`)
	snaplockLitigationNameRe          = regexp.MustCompile(`(?m)^\s*Litigation Name:\s*(.+)$`)
	snaplockVserverBlockRe            = regexp.MustCompile(`(?m)^\s*Vserver:\s*`)
	snaplockPathRe                    = regexp.MustCompile(`(?m)^\s*Path:\s*(.+)$`)
	snaplockOperationIDRe             = regexp.MustCompile(`(?m)^\s*Operation ID:\s*(\d+)`)
	snaplockOperationIDFromBeginEndRe = regexp.MustCompile(`-operation-id\s+(\d+)`)
)

// uuidPattern matches UUID-like segments in URL paths (8-4-4-4-12 hex).
var uuidPattern = regexp.MustCompile(`/[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)

// ontapPathParamPatterns identify ONTAP endpoints whose trailing path parameter is a
// filesystem path that must be percent-encoded before forwarding. Group 1 is the structural
// prefix (kept verbatim); group 2 is the path parameter (re-encoded).
var ontapPathParamPatterns = []*regexp.Regexp{
	// /api/storage/volumes/{volume.uuid}/files/{path}
	regexp.MustCompile(`^(/api/storage/volumes/[^/]+/files/)(.+)$`),
}

// ontapErrorResponse matches the ONTAP REST API error response JSON (same shape as handlers.OntapErrorResponse).
type ontapErrorResponse struct {
	Error *ontapError `json:"error,omitempty"`
}

type ontapError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// LitigationRecord holds parsed litigation name and path from "snaplock legal-hold show -instance" output.
type LitigationRecord struct {
	Name string
	Path string
}

// OperationStatusRecord holds parsed operation status from "snaplock legal-hold show -operation-id X -instance"
// or from each block of "snaplock legal-hold show -litigation-name X -instance" / "snaplock legal-hold show -volume X -instance".
type OperationStatusRecord struct {
	LitigationName    string
	OperationID       int
	Status            string // Completed, In-Progress, Failed, Aborting
	Path              string
	OperationType     string // begin, end
	NumFilesProcessed string
	NumFilesFailed    string
	NumFilesSkipped   string
	NumInodesIgnored  string
	StatusDetails     string
}

// extractOntapPathRaw returns the path segment after OntapAPISegment, without normalization.
func extractOntapPathRaw(fullPath string) string {
	parts := strings.Split(fullPath, "/")

	ontapApiIndex := -1
	for i, part := range parts {
		if part == OntapAPISegment {
			ontapApiIndex = i
			break
		}
	}

	if ontapApiIndex == -1 {
		return ""
	}

	return "/" + strings.Join(parts[ontapApiIndex+1:], "/")
}

// ExtractOntapPath returns the ONTAP path from the full request path with UUIDs normalized to {uuid}
// for stable path matching (e.g. credential and rule engine). Use ExtractOntapPathRaw when forwarding
// the path to ONTAP.
func ExtractOntapPath(fullPath string) string {
	ontapPath := extractOntapPathRaw(fullPath)
	if ontapPath == "" {
		return ""
	}
	return NormalizeUUIDs(ontapPath)
}

// ExtractOntapPathRaw returns the ONTAP path from the full request path without normalization.
// Use this when setting the request path sent to ONTAP (e.g. in the reverse proxy).
func ExtractOntapPathRaw(fullPath string) string {
	return extractOntapPathRaw(fullPath)
}

// NormalizeUUIDs replaces UUID-like path segments with {uuid} for stable matching.
func NormalizeUUIDs(path string) string {
	return uuidPattern.ReplaceAllString(path, "/{uuid}")
}

// EncodeOntapPathParam percent-encodes an ONTAP path parameter ("/"->"%2F", "."->"%2E") so
// ONTAP's RFC 3986 dot-segment resolution treats it literally. url.PathEscape handles "/" and
// other unsafe chars but leaves "." alone, so we encode dots ourselves.
func EncodeOntapPathParam(p string) string {
	return strings.ReplaceAll(url.PathEscape(p), ".", "%2E")
}

// ReencodeOntapFilePath re-encodes only the trailing path parameter of a matching ONTAP
// volumes/files endpoint, keeping the prefix verbatim. Returns "" when the path does not
// match that endpoint, or when encoding would not change ontapPath (callers should leave
// URL.RawPath unset so Go uses the default path encoding).
func ReencodeOntapFilePath(ontapPath string) string {
	for _, re := range ontapPathParamPatterns {
		if m := re.FindStringSubmatch(ontapPath); m != nil {
			encoded := m[1] + EncodeOntapPathParam(m[2])
			if encoded == ontapPath {
				return ""
			}
			return encoded
		}
	}
	return ""
}

// WriteErrorResponse writes a JSON error response with code and message to the ResponseWriter.
func WriteErrorResponse(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	err := json.NewEncoder(w).Encode(&ontapserver.Error{
		Code:    code,
		Message: message,
	})
	if err != nil {
		// Fallback: write message as plain text directly to body
		// Note: Headers and status code are already written, so we can only write the body
		// Content-Type will remain "application/json" but body will be plain text
		_, _ = w.Write([]byte(message))
	}
}

// ParseSizeString parses a size string (e.g. "10g", "100m", "1024", "10.5GB") into bytes.
// Supports units: K/KB, M/MB, G/GB, T/TB, P/PB (case-insensitive, base-1024).
// Decimal numbers are allowed (e.g. "10.5g"). Leading + or - is not allowed; size must be positive.
// If invalid, returns 0.
func ParseSizeString(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		return 0
	}
	var numPart string
	var unitPart string
	for i, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			numPart = s[:i]
			unitPart = strings.TrimSpace(strings.ToUpper(s[i:]))
			break
		}
	}
	if numPart == "" {
		numPart = s
	}
	val, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0
	}
	var mult float64
	switch unitPart {
	case "":
		mult = 1
	case "K", "KB":
		mult = 1024
	case "M", "MB":
		mult = 1024 * 1024
	case "G", "GB":
		mult = 1024 * 1024 * 1024
	case "T", "TB":
		mult = 1024 * 1024 * 1024 * 1024
	case "P", "PB":
		mult = 1024 * 1024 * 1024 * 1024 * 1024
	default:
		return 0
	}
	result := val * mult
	if result <= 0 {
		return 0
	}
	return result
}

// ParseOntapErrorBody parses an ONTAP REST API error response body and returns the error code and message.
func ParseOntapErrorBody(body []byte) (code int, message string) {
	if len(body) == 0 {
		return 0, ""
	}
	var parsed ontapErrorResponse
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Error == nil {
		return 0, ontapErrorFallbackMessage
	}
	if parsed.Error.Message != "" {
		message = parsed.Error.Message
	} else {
		message = ontapErrorFallbackMessage
	}
	if parsed.Error.Code != "" {
		if c, err := strconv.Atoi(parsed.Error.Code); err == nil {
			code = c
		}
	}
	return code, message
}

// StripOntapLoginBanner removes the ONTAP "first recorded login" message from CLI output
// so it is not shown in API responses. Handles any amount of surrounding newlines or whitespace.
func StripOntapLoginBanner(output string) string {
	if output == "" {
		return output
	}
	s := ontapFirstLoginBannerRe.ReplaceAllString(output, "")
	return strings.TrimLeft(s, "\r\n")
}

func splitInstanceBlocks(output string) []string {
	parts := snaplockVserverBlockRe.Split(output, -1)
	if len(parts) <= 1 {
		if strings.TrimSpace(output) != "" {
			return []string{output}
		}
		return nil
	}
	var blocks []string
	for _, p := range parts[1:] {
		if strings.TrimSpace(p) != "" {
			blocks = append(blocks, p)
		}
	}
	return blocks
}

func extractInstanceField(block string, re *regexp.Regexp) string {
	m := re.FindStringSubmatch(block)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// ParseSnaplockLegalHoldShowInstanceOutput parses "snaplock legal-hold show -instance" output and returns
// unique litigations (by name) for the volume. Each block has Litigation Name and Path; we dedupe by name.
func ParseSnaplockLegalHoldShowInstanceOutput(output string) ([]LitigationRecord, error) {
	output = StripOntapLoginBanner(output)
	seen := make(map[string]string)
	blocks := splitInstanceBlocks(output)
	for _, block := range blocks {
		name := extractInstanceField(block, snaplockLitigationNameRe)
		path := extractInstanceField(block, snaplockPathRe)
		if name == "" {
			continue
		}
		if path == "" {
			path = "/"
		}
		if _, ok := seen[name]; !ok {
			seen[name] = path
		}
	}
	var out []LitigationRecord
	for n, p := range seen {
		out = append(out, LitigationRecord{Name: n, Path: p})
	}
	return out, nil
}

// ParseOperationIDFromBeginEndOutput extracts the operation ID from the output of
// snaplock legal-hold begin or end. Returns 0, false if not found.
func ParseOperationIDFromBeginEndOutput(output string) (int, bool) {
	output = StripOntapLoginBanner(output)
	matches := snaplockOperationIDFromBeginEndRe.FindStringSubmatch(output)
	if len(matches) < 2 {
		return 0, false
	}
	var id int
	if _, err := fmt.Sscanf(matches[1], "%d", &id); err != nil {
		return 0, false
	}
	return id, true
}

// ParseSnaplockLegalHoldShowOperationOutput parses "snaplock legal-hold show -operation-id X -instance" output
// and returns the single operation block. Returns nil if no operation block is found.
func ParseSnaplockLegalHoldShowOperationOutput(output string) (*OperationStatusRecord, error) {
	output = StripOntapLoginBanner(output)
	blocks := splitInstanceBlocks(output)
	if len(blocks) == 0 {
		if strings.TrimSpace(output) != "" {
			blocks = []string{output}
		}
	}
	if len(blocks) == 0 {
		return nil, nil
	}
	block := blocks[0]
	idStr := extractInstanceField(block, snaplockOperationIDRe)
	if idStr == "" {
		return nil, nil
	}
	var opID int
	if _, err := fmt.Sscanf(idStr, "%d", &opID); err != nil {
		return nil, nil
	}
	rec := &OperationStatusRecord{
		OperationID:       opID,
		Status:            strings.TrimSpace(extractInstanceField(block, snaplockStatusRe)),
		Path:              strings.TrimSpace(extractInstanceField(block, snaplockPathRe)),
		OperationType:     strings.TrimSpace(strings.ToLower(extractInstanceField(block, snaplockOperationTypeRe))),
		NumFilesProcessed: strings.TrimSpace(extractInstanceField(block, snaplockNumFilesProcessedRe)),
		NumFilesFailed:    strings.TrimSpace(extractInstanceField(block, snaplockNumFilesFailedRe)),
		NumFilesSkipped:   strings.TrimSpace(extractInstanceField(block, snaplockNumFilesSkippedRe)),
		NumInodesIgnored:  strings.TrimSpace(extractInstanceField(block, snaplockNumInodesIgnoredRe)),
		StatusDetails:     strings.TrimSpace(extractInstanceField(block, snaplockStatusDetailsRe)),
	}
	return rec, nil
}

// ParseSnaplockLegalHoldShowInstanceOutputToOperations parses "snaplock legal-hold show -litigation-name X -instance"
// output and returns all operation blocks (Operation ID, Status, Path, Type, etc.).
func ParseSnaplockLegalHoldShowInstanceOutputToOperations(output string) []*OperationStatusRecord {
	output = StripOntapLoginBanner(output)
	blocks := splitInstanceBlocks(output)
	if len(blocks) == 0 {
		if strings.TrimSpace(output) != "" {
			blocks = []string{output}
		}
	}
	var out []*OperationStatusRecord
	for _, block := range blocks {
		idStr := extractInstanceField(block, snaplockOperationIDRe)
		if idStr == "" {
			continue
		}
		var opID int
		if _, err := fmt.Sscanf(idStr, "%d", &opID); err != nil {
			continue
		}
		litName := strings.TrimSpace(extractInstanceField(block, snaplockLitigationNameRe))
		rec := &OperationStatusRecord{
			LitigationName:    litName,
			OperationID:       opID,
			Status:            strings.TrimSpace(extractInstanceField(block, snaplockStatusRe)),
			Path:              strings.TrimSpace(extractInstanceField(block, snaplockPathRe)),
			OperationType:     strings.TrimSpace(strings.ToLower(extractInstanceField(block, snaplockOperationTypeRe))),
			NumFilesProcessed: strings.TrimSpace(extractInstanceField(block, snaplockNumFilesProcessedRe)),
			NumFilesFailed:    strings.TrimSpace(extractInstanceField(block, snaplockNumFilesFailedRe)),
			NumFilesSkipped:   strings.TrimSpace(extractInstanceField(block, snaplockNumFilesSkippedRe)),
			NumInodesIgnored:  strings.TrimSpace(extractInstanceField(block, snaplockNumInodesIgnoredRe)),
			StatusDetails:     strings.TrimSpace(extractInstanceField(block, snaplockStatusDetailsRe)),
		}
		out = append(out, rec)
	}
	return out
}

// MapOperationStatusToState maps CLI Status value to API state (in_progress, failed, aborting, completed).
func MapOperationStatusToState(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "completed":
		return "completed"
	case "in-progress":
		return "in_progress"
	case "failed":
		return "failed"
	case "aborting":
		return "aborting"
	default:
		return "in_progress"
	}
}
