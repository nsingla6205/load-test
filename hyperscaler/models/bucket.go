package hyperscaler

// BucketDetails represents the details of a GCP storage bucket
type BucketDetails struct {
	Name          string `json:"name"`
	Location      string `json:"location"`
	LocationType  string `json:"locationType"`
	StorageClass  string `json:"storageClass"`
	SatisfiesPzi  bool   `json:"satisfiesPzi"` // Zone Isolation
	SatisfiesPzs  bool   `json:"satisfiesPzs"` // Zone Separation
	ProjectNumber string `json:"projectNumber"`
	Region        string `json:"region"`
	Created       string `json:"created"`
	Updated       string `json:"updated"`
}

type BucketFileDetails struct {
	BucketName     string `json:"bucketName"`
	FileUrl        string `json:"fileUrl"`
	FileHashSHA256 string `json:"fileHashSHA256"` // It contains base64 of SHA256 hash of the file stored in bucket
	FileContent    string `json:"fileContent"`
}
