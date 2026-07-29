package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	ghutils "main/cmd/github"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/spf13/cobra"
)

var (
	username      string
	password      string
	jiraServerUrl string
)

var ExpectedStatusMap = map[string]string{
	"Bug":            "In Development",
	"Story":          "In Development",
	"Documentation":  "In Progress",
	"RC/HF Approval": "In Development",
}

const DefaultUrl = "https://jira.ngage.netapp.com"
const jiraApiUser = "JIRA_API_USER"
const jiraApiToken = "JIRA_API_TOKEN"
const jiraServer = "JIRA_SERVER"

var Url string

var JiraCmd = &cobra.Command{
	Use:   "jira",
	Short: "A command to handle all jira functionalities",
}

type BaseURL string

type ClientCredentials struct {
	Username string
	Password string
}

func GetJiraUrlCredentials() (BaseURL, ClientCredentials) {
	if jiraServerUrl != "" {
		Url = jiraServerUrl
	} else {
		Url = DefaultUrl
	}

	jiraUrl := BaseURL(Url)
	credentials := ClientCredentials{
		Username: username,
		Password: password,
	}

	return jiraUrl, credentials
}

func ExtractJiraID(prTitle string) (string, error) {
	re := regexp.MustCompile(`^(VSCP-[0-9]+):`) // Assuming PR is of type VSCP-1234: <title>
	matches := re.FindStringSubmatch(prTitle)
	if len(matches) == 0 {
		return "", fmt.Errorf("PR title is not in the correct format. Required format: VSCP-<IssueNumber>: <title>\nCurrent PR title: %s", prTitle)
	}
	return matches[1], nil
}

func GetJiraInfo(query string) (string, error) {
	baseURL, _ := GetJiraUrlCredentials()

	encodedQuery := url.QueryEscape(query)
	jiraQueryURL := fmt.Sprintf("%s/issues/?jql=%s", baseURL, encodedQuery)

	return jiraQueryURL, nil
}

func GetJiraClient(credentials ClientCredentials, baseURL string) (*jira.Client, error) {
	tp := jira.BearerAuthTransport{
		Token: credentials.Password,
	}

	client, err := jira.NewClient(tp.Client(), baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jira client: %w", err)
	}
	return client, nil
}

func GetJiraIssue(jiraID string, client *jira.Client) (*jira.Issue, error) {
	issue, _, err := client.Issue.Get(jiraID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve Jira issue: %w", err)
	}

	return issue, nil
}

func CheckIssueStatus(issue *jira.Issue) error {
	// Check if the issue type has a valid expected status
	expectedStatus, exists := ExpectedStatusMap[issue.Fields.Type.Name]
	if !exists {
		return fmt.Errorf("issue type '%s' is not recognized", issue.Fields.Type.Name)
	}

	// Validate the issue status against the expected status
	if issue.Fields.Status.Name != expectedStatus {
		return fmt.Errorf("issue %s of type '%s' is in status '%s', but expected status is '%s'",
			issue.Key, issue.Fields.Type.Name, issue.Fields.Status.Name, expectedStatus)
	}

	log.Printf("Issue %s of type '%s' is in the expected status: '%s'.\n",
		issue.Key, issue.Fields.Type.Name, issue.Fields.Status.Name)
	return nil
}

func UpdateJiraWithPayload(updatedPayload map[string]interface{}, issueID string, credentials ClientCredentials, baseURL string) error {
	tp := jira.BearerAuthTransport{
		Token: credentials.Password,
	}

	client, err := jira.NewClient(tp.Client(), baseURL)
	if err != nil {
		return fmt.Errorf("failed to create Jira client: %w", err)
	}

	payload, _ := json.Marshal(updatedPayload)
	log.Printf("Request Payload: %s\n", string(payload))

	// Perform the update
	_, err = client.Issue.UpdateIssue(issueID, updatedPayload)
	if err != nil {
		return fmt.Errorf("failed to update payload: %w", err)
	}

	log.Println("Successfully updated JIRA issue")
	return nil
}

func ValidateAssigneeEmail(issue *jira.Issue) error {
	if issue.Fields.Assignee == nil || issue.Fields.Assignee.EmailAddress == "" {
		return fmt.Errorf("assignee or email address is missing")
	}

	emailAddress := NormalizeEmail(issue.Fields.Assignee.EmailAddress)
	log.Println("Email Address:", emailAddress)

	ghUser, err := ghutils.GetGithubUser(ghutils.GhToken, ghutils.PrUser)
	if err != nil {
		log.Println("Error:", err)
		os.Exit(1)
	}

	if ghUser.Email == nil || strings.TrimSpace(*ghUser.Email) == "" {
		log.Printf("GitHub user %s has no public email; skipping assignee email validation.", ghutils.PrUser)
		return nil
	}

	ghEmail := NormalizeEmail(*ghUser.Email)

	if emailAddress != ghEmail {
		return fmt.Errorf("email mismatch: authors do not match")
	}

	log.Println("Emails match. Validation successful.")
	return nil
}

func ValidateIssueType(issue *jira.Issue, expectedType string, errorMessage string) error {
	issueType := issue.Fields.Type.Name
	if issueType != expectedType {
		log.Printf("Error: %s (Expected: %s, Found: %s)\n", errorMessage, expectedType, issueType)
		return fmt.Errorf("issue type mismatch: expected %s, found %s", expectedType, issueType)
	}
	return nil
}

func ValidateCustomField(fieldsMap map[string]interface{}, fieldKey, expectedValue string) error {
	if approvalField, ok := fieldsMap[fieldKey].(map[string]interface{}); ok {
		if value, ok := approvalField["value"].(string); ok {
			log.Printf("Value of %s: %s\n", expectedValue, value)
			if value != "Yes" {
				return fmt.Errorf("%s should be marked Yes. Currently in state: %s", expectedValue, value)
			}
		} else {
			return fmt.Errorf("Value field in %s is not a string or is missing", expectedValue)
		}
	} else {
		return fmt.Errorf("%s is not a map or is missing", expectedValue)
	}
	return nil
}

func GetJiraFieldValue(issueID, fieldName string, credentials ClientCredentials, baseURL string) (interface{}, error) {
	// Construct the Jira API URL
	apiURL := fmt.Sprintf("%s/rest/api/2/issue/%s", baseURL, issueID)

	// Create a BearerAuthTransport for authentication
	tp := jira.BearerAuthTransport{
		Token: credentials.Password,
	}

	// Create an HTTP client using the transport
	client := tp.Client()

	// Create a new HTTP request
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set the required headers
	req.Header.Set("Accept", "application/json")

	// Execute the HTTP request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute HTTP request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Error closing file: %v", err)
		}
	}()

	// Check for non-200 status codes
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the JSON response
	var issueData map[string]interface{}
	err = json.Unmarshal(body, &issueData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	// Extract the field value
	fields, ok := issueData["fields"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to extract fields from response")
	}

	fieldValue, exists := fields[fieldName]
	if !exists {
		return nil, fmt.Errorf("field '%s' not found in issue", fieldName)
	}

	return fieldValue, nil
}

// NormalizeEmail converts the email to lower case
func NormalizeEmail(email string) string {
	email = strings.ToLower(email)
	return email
}

func extractMergedCount(customField string) (int, error) {
	// Define a regex pattern to extract mergedCount
	pattern := `mergedCount=(\d+)`
	re := regexp.MustCompile(pattern)

	// Find the first match
	matches := re.FindStringSubmatch(customField)
	if len(matches) < 2 {
		return 0, fmt.Errorf("mergedCount not found in customField")
	}

	// Convert the matched value to an integer
	var mergedCount int
	_, err := fmt.Sscanf(matches[1], "%d", &mergedCount)
	if err != nil {
		return 0, fmt.Errorf("failed to parse mergedCount: %w", err)
	}

	return mergedCount, nil
}

func ExtractFieldsAsMap(issue *jira.Issue) (map[string]interface{}, error) {
	// Marshal the Fields struct into JSON
	fieldsJSON, err := json.Marshal(issue.Fields)
	if err != nil {
		return nil, err
	}

	// Unmarshal the JSON into a map
	var fieldsMap map[string]interface{}
	err = json.Unmarshal(fieldsJSON, &fieldsMap)
	if err != nil {
		return nil, err
	}

	return fieldsMap, nil
}

func init() {
	username = os.Getenv(jiraApiUser)
	password = os.Getenv(jiraApiToken)
	jiraServerUrl = os.Getenv(jiraServer)

	JiraCmd.AddCommand(allowMergeCmd)
	JiraCmd.AddCommand(tagJiraComponentVersionCmd)
}
