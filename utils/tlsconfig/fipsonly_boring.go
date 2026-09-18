//go:build boringcrypto

package tlsconfig

// A production build using GOEXPERIMENT=boringcrypto must also restrict TLS to
// the validated module's approved algorithms. Merely compiling BoringCrypto
// into the binary does not enable this policy by itself.
import _ "crypto/tls/fipsonly"
