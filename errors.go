package main

// simpleErr 是最小错误类型，仅携带字符串消息。
type simpleErr string

func (e simpleErr) Error() string { return string(e) }
