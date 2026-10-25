// Package version 构建期注入的版本信息（Release workflow 通过 -ldflags -X 写入 tag 名）。
package version

// Version 当前程序版本；源码构建时为 dev，正式发布由 ldflags 注入（如 v1.2.3）。
var Version = "dev"
