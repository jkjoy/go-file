package model

import (
	"errors"
	"go-file/common"
	"strconv"
	"strings"
)

type Option struct {
	Key   string `json:"key" gorm:"primaryKey"`
	Value string `json:"value"`
}

func AllOption() ([]*Option, error) {
	var options []*Option
	var err error
	err = DB.Find(&options).Error
	return options, err
}

func InitOptionMap() {
	common.OptionMap = make(map[string]string)
	common.OptionMap["FileUploadPermission"] = strconv.Itoa(common.FileUploadPermission)
	common.OptionMap["FileDownloadPermission"] = strconv.Itoa(common.FileDownloadPermission)
	common.OptionMap["ImageUploadPermission"] = strconv.Itoa(common.ImageUploadPermission)
	common.OptionMap["ImageDownloadPermission"] = strconv.Itoa(common.ImageDownloadPermission)
	common.OptionMap["WebsiteName"] = "Go File"
	common.OptionMap["WebsiteTheme"] = common.DefaultThemeID
	common.OptionMap["FooterInfo"] = ""
	common.OptionMap["Version"] = common.Version
	common.OptionMap["Notice"] = ""
	common.OptionMap["StatEnabled"] = strconv.FormatBool(common.StatEnabled)
	options, _ := AllOption()
	for _, option := range options {
		updateOptionMap(option.Key, option.Value)
	}
}

func UpdateOption(key string, value string) error {
	if key == "WebsiteTheme" && !common.IsValidTheme(value) {
		return errors.New("无效的主题模板")
	}
	if key == "StatEnabled" && value != "true" && value != "false" {
		return errors.New("无效的统计开关")
	}

	// Save to database first
	option := Option{
		Key:   key,
		Value: value,
	}
	// When updating with struct it will only update non-zero fields by default
	// So we have to use Select here
	if DB.Model(&option).Where("key = ?", key).Update("value", option.Value).RowsAffected == 0 {
		DB.Create(&option)
	}
	// Update OptionMap
	updateOptionMap(key, value)
	return nil
}

func updateOptionMap(key string, value string) {
	if key == "WebsiteTheme" && !common.IsValidTheme(value) {
		value = common.DefaultThemeID
	}
	common.OptionMap[key] = value
	if strings.HasSuffix(key, "Permission") {
		intValue, _ := strconv.Atoi(value)
		switch key {
		case "FileUploadPermission":
			common.FileUploadPermission = intValue
		case "FileDownloadPermission":
			common.FileDownloadPermission = intValue
		case "ImageUploadPermission":
			common.ImageUploadPermission = intValue
		case "ImageDownloadPermission":
			common.ImageDownloadPermission = intValue
		}
	}
	if key == "StatEnabled" {
		common.StatEnabled = value == "true"
	}
}
