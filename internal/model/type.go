package model

type Account struct {
	AccountId   string   `json:"accountId"`
	AccountName string   `json:"accountName"`
	AccountType string   `json:"accountType"`
	Enable      bool     `json:"enable"`
	FourAUser   string   `json:"4AUserName"`
	Email       string   `json:"email"`
	PhoneNumber string   `json:"phoneNumber"`
	Created     string   `json:"created"`
	ActiveDate  string   `json:"activeDate"`
	ExpireDate  string   `json:"expireDate"`
	RoleCode    []string `json:"roleCode"`
}

type AccountsWrapper struct {
	TotalCount int       `json:"totalCount"`
	PageNum    int       `json:"pageNum"`
	TotalPage  int       `json:"totalPage"`
	Accounts   []Account `json:"accounts"`
}

type Role struct {
	RoleCode        string `json:"roleCode"`
	RoleName        string `json:"roleName"`
	RoleDescription string `json:"roleDescription"`
}
