# ORP NGINX 配置导入器

独立的 Go API 与 Vue 界面，用于保存 `nginx -T` 导出的配置、预览转换结果，并通过现有控制面 API 将支持的 HTTP 资源导入 ORP。

## 本地运行

```sh
cd orp-nginx-importer/web && npm install
cd .. && ./dev.sh
```

启动后访问 <http://localhost:8091>。Vite 会将 `/api` 请求代理到 `:8090`；Go 服务也可以直接提供 `web/dist` 中的生产版界面。

| 环境变量 | 用途 | 默认值 |
| --- | --- | --- |
| `NGINX_IMPORTER_ADDR` | Go 服务监听地址 | `:8090` |
| `NGINX_IMPORTER_STORAGE` | 配置源文件与导入批次的持久化目录 | `./data` |
| `ORP_BACKEND_URL` | ORP 控制面地址 | `http://127.0.0.1:8081` |

请勿将存储目录放在公开 Web 根目录下。

## 主机采集脚本

页面提供 curl 与 wget 两种可复制命令。`/scripts/send_nginx_config_script.sh` 返回嵌入式采集脚本的 Base64 编码，命令通过 `base64 -d` 解码后交给 Bash 执行。Go 服务将 [采集脚本](scripts/send_nginx_config_script.sh) 嵌入程序，无需额外挂载静态文件。脚本扫描主机 NGINX master 进程和运行中的 Docker 容器，并上传配置到脚本中的 API 地址（可用 `NGINX_CONFIG_API_BASE_URL` 覆盖，默认是脚本内的测试地址）。执行前请检查脚本内容，并确认目标主机的进程读取和 Docker 权限。

## 导入流程

1. 使用 `nginx -T > nginx-T.txt` 导出配置并上传，单个文件最大 32 MB。
2. 原始文件保存在 `data/sources/`；SHA-256 摘要与解析预览保存在 `data/batches/`。
3. 检查识别出的资源与不支持的指令，填写 ORP Center UUID 和有权限的 Bearer Token，将支持的 HTTP Upstream、目标、Server 与简单 Location 导入 ORP。
4. 在 ORP 中复核导入资源和不支持项，再创建发布。导入操作不会发布配置、在节点上校验、重载或重启服务。

## 配置转换范围

当前支持映射 HTTP Upstream 的 `server` 目标及 `weight`/`backup`、HTTP `server_name`、监听端口、简单 `location`、引用具名 Upstream 的 `proxy_pass`、`root`、`alias` 和 `return`。未识别的指令会连同来源文件、行号和上下文保存在不支持项报告中。

TLS 监听器会标记为需要手动映射证书；包含 TLS Server 的批次会被服务端拒绝导入，避免把原 HTTPS 监听误建为明文 HTTP。存在未支持项时，导入请求必须显式确认遗漏；变量、复杂 Location 匹配、Upstream 策略、导出文件中缺失的 `include` 文件、任意模块、Stream/Mail 配置，以及上述映射范围以外的指令语义仍需人工处理。解析器不会执行 NGINX 命令，也不会访问上传配置中引用的路径。

同一批次的导入预留使用状态目录内的文件锁协调共享该目录的多个 importer 进程，并通过临时文件加原子替换持久化预留状态。若某个资源写入后失败，批次会保留为已预留状态，避免重试造成重复资源；需先在 ORP 中人工核对部分结果。状态目录需位于支持本机文件锁语义的共享文件系统上。

批量写入通过 ORP CRUD 接口完成，多个请求不是一个事务。如果 ORP 在后续请求中拒绝资源，之前成功写入的资源可能已经存在；重试前请检查导入批次和 ORP 中的资源。
