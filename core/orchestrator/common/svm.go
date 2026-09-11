package common

import "fmt"

// DeriveVCMSvmName returns the data SVM name for a VCM deployment ({deploymentId}-svm-01).
func DeriveVCMSvmName(deploymentName string) (string, error) {
	if deploymentName == "" {
		return "", fmt.Errorf("VCM deployment ID is required to get the data SVM")
	}
	return deploymentName + "-svm-01", nil
}
