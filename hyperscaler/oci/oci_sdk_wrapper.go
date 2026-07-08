package oci

// ociSDKWrapper is the OCI Go SDK implementation of ociWrapper
type ociSDKWrapper struct {
	svc *OciServices
}

// Compile-time assertion that ociSDKWrapper satisfies the ociWrapper Strategy.
var _ ociWrapper = (*ociSDKWrapper)(nil)

// activeOCIWrapper returns the OCI operation wrapper the public OciServices
// methods delegate to.
func (s *OciServices) activeOCIWrapper() ociWrapper {
	if s.wrapper != nil {
		return s.wrapper
	}
	return &ociSDKWrapper{svc: s}
}
