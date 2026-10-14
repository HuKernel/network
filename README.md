# 局域网文件传输（LAN Share）

面向局域网的安全文件传输与共享系统：一台设备作为主机，其他设备通过浏览器访问，无需安装客户端。支持账号体系、权限控制、IP/设备封禁、共享文件夹、回收站与审计日志。

## 开发模式

```powershell
.\start.ps1
```

后端 `go run`（http://localhost:8080），前端 Vite 热更新（http://localhost:5173，`/api` 自动代理到 8080）。

## 构建单文件程序

```powershell
.\build.ps1
```

前端构建产物会内嵌进 Go 二进制，生成 `release\lan-share.exe`。把它拷到任意机器直接运行即可（无需 Node/Go 环境）：

```
lan-share.exe                    # 默认监听 :8080，数据存放在当前目录
lan-share.exe -addr :9000        # 指定端口
lan-share.exe -data D:\lan-data  # 指定数据目录（数据库、上传文件、共享文件夹、回收站）
```

其他设备浏览器访问 `http://<主机IP>:8080`。首次启动需创建管理员账号。

## 数据目录

运行时在数据目录下创建：`data/`（SQLite 数据库）、`storage/`（上传文件）、`shared-folders/`、`trash/`。备份这几个目录即备份全部数据。

## 结构

- `backend/cmd/lan-share/` — Go 后端（Gin + SQLite，纯 Go 无 CGO，可交叉编译）
- `backend/cmd/lan-share/web/` — 前端构建产物内嵌目录（由 build.ps1 生成，仓库只保留占位页）
- `frontend/` — Vue 3 + Vite 前端
- 开发文档见 `局域网文件传输开发文档.md`
