package model

import (
	"strings"
	"unicode"
)


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
	BatchNo    string    `json:"batchNo"`
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

type AppAccountDiffResponse struct {
        TotalResults int               `json:"totalResults"`
	StartIndex   int               `json:"startIndex"`
	ItemsPerPage int               `json:"itemsPerPage"`
	Resources []ResourceAccount `json:"Resources"`
}

type ResourceAccount struct {
	AppAccount
	DiffType string `json:"diffType"`
}

type AppAccount struct {
	AccountName string   `json:"accountName"`
	Enable      bool     `json:"enable"`
	PhoneNumber string   `json:"phone"`
	ExpireDate  string   `json:"expireDate"`
	RoleCode    []string `json:"roleCodes"`
}

func (a *AppAccount) Mobile() string {
	return NormalizePhone(a.PhoneNumber)
}

func NormalizePhone(phone string) string {
	// 1. 只保留数字
	var digits []rune
	for _, r := range phone {
		if unicode.IsDigit(r) {
			digits = append(digits, r)
		}
	}

	num := string(digits)

	// 2. 去掉国家码 86
	if strings.HasPrefix(num, "86") && len(num) > 11 {
		num = num[2:]
	}

	// 3. 如果长度大于11，取最后11位
	if len(num) > 11 {
		num = num[len(num)-11:]
	}

	return num
}
