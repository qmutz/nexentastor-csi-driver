package ns

import (
	"strings"
)

// ACLRuleSet - filesystem ACL rule set
type ACLRuleSet int64

const (
	// ACLReadOnly - apply read only set of rules to filesystem
	ACLReadOnly ACLRuleSet = iota

	// ACLReadWrite - apply full access set of rules to filesystem
	ACLReadWrite
)

// License - NexentaStor license
type License struct {
	Valid   bool   `json:"valid"`
	Expires string `json:"expires"`
}

// Filesystem - NexentaStor filesystem
type Filesystem struct {
	Path           string `json:"path"`
	MountPoint     string `json:"mountPoint"`
	SharedOverNfs  bool   `json:"sharedOverNfs"`
	SharedOverSmb  bool   `json:"sharedOverSmb"`
	BytesAvailable int64  `json:"bytesAvailable"`
	BytesUsed      int64  `json:"bytesUsed"`
}

// GetDefaultSmbShareName - get default SMB share name (all slashes get replaced by underscore)
// Converts '/pool/dataset/fs' to 'pool_dataset_fs'
func (fs *Filesystem) GetDefaultSmbShareName() string {
	return strings.Replace(strings.TrimPrefix(fs.Path, "/"), "/", "_", -1)
}

// GetReferencedQuotaSize - get total referenced quota size
func (fs *Filesystem) GetReferencedQuotaSize() int64 {
	return fs.BytesAvailable + fs.BytesUsed
}

// NEF request/response types

type nefAuthLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type nefAuthLoginResponse struct {
	Token string `json:"token"`
}

type nefStoragePoolsResponse struct {
	Data []nefStoragePoolsResponsePool `json:"data"`
}
type nefStoragePoolsResponsePool struct {
	PoolName string `json:"poolName"`
}

type nefStorageFilesystemsResponse struct {
	Data []Filesystem `json:"data"`
}

type nefNasNfsRequest struct {
	Filesystem       string                            `json:"filesystem"`
	Anon             string                            `json:"anon"`
	SecurityContexts []nefNasNfsRequestSecurityContext `json:"securityContexts"`
}
type nefNasNfsRequestSecurityContext struct {
	SecurityModes []string `json:"securityModes"`
}

type nefNasSmbResponse struct {
	ShareName string `json:"shareName"`
}

type nefStorageFilesystemsACLRequest struct {
	Type        string   `json:"type"`
	Principal   string   `json:"principal"`
	Flags       []string `json:"flags"`
	Permissions []string `json:"permissions"`
}

type nefJobStatusResponse struct {
	Links []nefJobStatusResponseLink `json:"links"`
}
type nefJobStatusResponseLink struct {
	Rel  string `json:"rel"`
	Href string `json:"href"`
}
