package vcm

import "fmt"

// deriveSvmName returns the data SVM name for a deployment ({deploymentId}-svm-01).
func deriveSvmName(deploymentName string) (string, error) {
	if deploymentName == "" {
		return "", fmt.Errorf("VCM deployment ID is required to get the data SVM")
	}
	return deploymentName + "-svm-01", nil
}
