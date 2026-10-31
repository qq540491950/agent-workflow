# 02: 请求来源与媒体类型边界

**What to build:** 服务器模式不仅要求身份，还校验请求从哪里来、发往哪个 Host、用什么媒体类型。恶意网页不能再借用户的浏览器向本机 API 发起写请求，DNS rebinding 形态也会被 Host 校验挡住；错误以稳定的 403/415 错误码返回，前端给出中文提示。

**Blocked by:** 01 服务模式安全默认与远程令牌.

**Status:** ready-for-agent

- [x] Host 头必须匹配受信监听地址；不匹配返回 403 `FORBIDDEN_ORIGIN`
- [x] 带 Origin 的请求必须与同源 SPA 或显式白名单匹配；跨源写请求返回 403；无 Origin 的本机 CLI 客户端放行
- [x] JSON 写接口要求 `application/json`；工作流导入允许 YAML；其他媒体类型返回 415 `UNSUPPORTED_MEDIA_TYPE`
- [x] 错误体统一为 `{error, code, kind}`，前端错误码表包含新增码的中文提示
- [x] 回环与远程模式使用同一套来源校验；桌面模式不经过该中间件
- [x] 负路径测试覆盖错误 Host、跨源 Origin、错误 Content-Type；正向同源请求通过
