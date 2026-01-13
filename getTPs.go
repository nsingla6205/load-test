package main

import (
	"context"
	"fmt"
	"log"

	billing "cloud.google.com/go/billing/apiv1"
	"cloud.google.com/go/billing/apiv1/billingpb"
	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v3"
)

func getActiveTenantProjects(ctx context.Context, parentFolderID string, maxTPs int) ([]string, error) {
	projectsService, err := cloudresourcemanager.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create projects client: %v", err)
	}

	filter := fmt.Sprintf("parent.type:folder parent.id:%s", parentFolderID)
	call := projectsService.Projects.Search().Query(filter)

	var activeProjects []string
	pageToken := ""
	tpCount := 0

	for {
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("failed to list projects: %v", err)
		}
		for _, project := range resp.Projects {
			enabled, err := isBillingEnabled(ctx, project.ProjectId)
			if err != nil {
				log.Printf("Failed to check billing for project %s: %v", project.ProjectId, err)
				continue
			}
			if enabled {
				activeProjects = append(activeProjects, project.ProjectId)
				log.Printf("✓ Active project with billing enabled: %s", project.ProjectId)
				tpCount++
				if maxTPs > 0 && tpCount >= maxTPs {
					return activeProjects, nil
				}
			} else {
				log.Printf("✗ Billing not enabled for project: %s", project.ProjectId)
			}
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	log.Printf("Active projects found: %d", len(activeProjects))
	return activeProjects, nil
}

func isBillingEnabled(ctx context.Context, projectID string) (bool, error) {
	client, err := billing.NewCloudBillingClient(ctx)
	if err != nil {
		return false, err
	}
	defer func(client *billing.CloudBillingClient) {
		err := client.Close()
		if err != nil {
			log.Printf("Failed to close billing client: %v", err)
		}
	}(client)

	req := &billingpb.GetProjectBillingInfoRequest{
		Name: "projects/" + projectID,
	}

	info, err := client.GetProjectBillingInfo(ctx, req)
	if err != nil {
		return false, err
	}

	return info.BillingEnabled, nil
}
