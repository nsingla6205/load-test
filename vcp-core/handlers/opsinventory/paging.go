package opsinventory

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPageSize = 10
	maxPageSize     = 50
)

var (
	errBadPageToken      = errors.New("invalid pageToken")
	errPageTokenMismatch = errors.New("pageToken does not match the request filters")
)

// pageToken holds the last row id of the previous page and a hash of the
// filters it was issued for. It is base64 JSON, not signed or encrypted: a
// caller can decode the internal row id. This is accepted for an internal
// admin API and documented in the OpenAPI description.
type pageToken struct {
	AfterID    int64  `json:"id"`
	FilterHash string `json:"f"`
}

func encodePageToken(afterID int64, filterHash string) string {
	b, _ := json.Marshal(pageToken{AfterID: afterID, FilterHash: filterHash})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodePageToken(token, filterHash string) (int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, errBadPageToken
	}
	var t pageToken
	if err := json.Unmarshal(b, &t); err != nil || t.AfterID <= 0 {
		return 0, errBadPageToken
	}
	if t.FilterHash != filterHash {
		return 0, errPageTokenMismatch
	}
	return t.AfterID, nil
}

// listFilterKey is the normalized set of list filters a page token is bound to.
type listFilterKey struct {
	resourceType   string
	projectNumber  string
	parents        []string
	timeFilters    []string
	fieldFilters   map[string]string
	includeDeleted bool
}

func (k listFilterKey) hash() string {
	parents := append([]string(nil), k.parents...)
	sort.Strings(parents)
	times := append([]string(nil), k.timeFilters...)
	sort.Strings(times)
	keys := make([]string, 0, len(k.fieldFilters))
	for key := range k.fieldFilters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := make([]string, 0, len(keys))
	for _, key := range keys {
		fields = append(fields, strconv.Quote(key)+"="+strconv.Quote(k.fieldFilters[key]))
	}

	canonical := strings.Join([]string{
		"rt=" + strconv.Quote(k.resourceType),
		"p=" + strconv.Quote(k.projectNumber),
		"parents=" + strings.Join(quoteAll(parents), ","),
		"times=" + strings.Join(quoteAll(times), ","),
		"fields=" + strings.Join(fields, ","),
		"deleted=" + strconv.FormatBool(k.includeDeleted),
	}, "|")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:8])
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strconv.Quote(s)
	}
	return out
}

func formatTimeFilter(field string, from, to *time.Time) string {
	bound := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339Nano)
	}
	return field + ":[" + bound(from) + "," + bound(to) + ")"
}
