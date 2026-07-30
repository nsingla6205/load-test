package rules_v2

import (
	"fmt"
	"net/http"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/ruleengine/dsl"
	proxyutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/utils"
)

// nestedString returns body[outer][inner] as a string, or "" when absent.
func nestedString(body map[string]interface{}, outer, inner string) string {
	if obj, ok := body[outer].(map[string]interface{}); ok && obj != nil {
		if v, ok := obj[inner].(string); ok {
			return v
		}
	}
	return ""
}

// rejectReservedQoSPolicyCreate denies POST /api/storage/qos/policies when the requested policy
// name is the reserved vserver-wide QPG (name == {svm.name}-qos-policy).
func rejectReservedQoSPolicyCreate(r *http.Request) (bool, string) {
	body, parseErr := dsl.GetParsedBody(r)
	if parseErr != "" {
		return false, parseErr
	}
	name, _ := body["name"].(string)
	svmName := nestedString(body, "svm", "name")
	if proxyutils.IsReservedQoSPolicyName(name, svmName) {
		return false, fmt.Sprintf("QoS policy %q uses a name that is reserved. Please create with a different name", name)
	}
	return true, ""
}

// rejectFixedQoSMinimumThroughput denies fixed QoS policy creation with a throughput floor.
func rejectFixedQoSMinimumThroughput(r *http.Request) (bool, string) {
	body, parseErr := dsl.GetParsedBody(r)
	if parseErr != "" {
		return false, parseErr
	}
	fixed, ok := body["fixed"].(map[string]interface{})
	if !ok || fixed == nil {
		return true, ""
	}
	if _, set := fixed["min_throughput_iops"]; set {
		return false, "fixed QoS minimum throughput is not supported"
	}
	if _, set := fixed["min_throughput_mbps"]; set {
		return false, "fixed QoS minimum throughput is not supported"
	}
	return true, ""
}
