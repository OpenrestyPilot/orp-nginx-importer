#!/usr/bin/env bash
# 开启未定义变量检查和管道失败检查；允许某个实例采集失败后继续处理其他实例。
set -uo pipefail

# 默认使用测试环境地址；部署到其他环境时可通过 NGINX_CONFIG_API_BASE_URL 覆盖。
API_BASE_URL="${NGINX_CONFIG_API_BASE_URL:-https://test.mg.tf.cn/sec/net-relation/api}"
UPLOAD_URL="${API_BASE_URL%/}/v1/nginx/RDYZ8nx833wCpad6"
HOSTNAME_VALUE="$(hostname)"
# 优先读取本机所有非回环地址，hostname -I 不可用时再用 ip 命令兜底。
HOST_IPS="$(hostname -I 2>/dev/null | xargs || true)"
if [[ -z "$HOST_IPS" ]] && command -v ip >/dev/null 2>&1; then
  HOST_IPS="$(ip -o addr show scope global | awk '{split($4,a,"/"); print a[1]}' | xargs || true)"
fi
if [[ -z "$HOST_IPS" ]]; then
  echo "无法获取本机非回环IP，停止上传。" >&2
  exit 1
fi

# 临时文件只存放待上传配置和 Base64 内容，脚本退出时统一清理。
TMP_DIR="$(mktemp -d)" || exit 1
trap 'rm -rf "$TMP_DIR"' EXIT
SUCCESS_COUNT=0
FAIL_COUNT=0

upload_config() {
  local instance_id="$1" label="$2" config_file="$3" encoded_file response status body
  encoded_file="$TMP_DIR/config-$(date +%s%N 2>/dev/null || date +%s)-$RANDOM.b64"
  # 服务端沿用 multipart 表单接口；instance_id 用来区分同一主机上的不同 Nginx 实例。
  base64 < "$config_file" | tr -d '\r\n' > "$encoded_file"
  if response="$(curl --silent --show-error --location --max-time 120 \
      --write-out '\n__HTTP_STATUS__%{http_code}' \
      -F "hostname=$HOSTNAME_VALUE" \
      -F "host_ips=$HOST_IPS" \
      -F "instance_id=$instance_id" \
      -F "file=@$encoded_file;filename=nginx-config.b64" \
      "$UPLOAD_URL")"; then
    status="$(printf '%s\n' "$response" | sed -n '$s/^__HTTP_STATUS__//p')"
    body="${response%$'\n__HTTP_STATUS__'*}"
    if [[ "$status" =~ ^2[0-9][0-9]$ ]]; then
      echo "上传成功：$label ($instance_id) $body"
      SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
    else
      echo "服务端拒绝配置：$label ($instance_id)，HTTP $status" >&2
      [[ -n "$body" ]] && printf '服务端响应：%s\n' "$body" >&2
      FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
  else
    echo "上传请求失败：$label ($instance_id)，请检查服务器地址、网络、curl 和服务端日志。" >&2
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi
}

collect_host_master() {
  local pid="$1" cgroup exe config_file process_args process_cwd custom_config custom_prefix instance_id identity
  [[ -r "/proc/$pid/cmdline" ]] || return 0
  cgroup="$(cat "/proc/$pid/cgroup" 2>/dev/null || true)"
  # 容器进程由下面的 docker exec 流程采集，避免宿主机视角执行容器二进制失败。
  if [[ "$cgroup" == *docker* || "$cgroup" == *containerd* || "$cgroup" == *kubepods* || "$cgroup" == *libpod* ]]; then
    echo "主进程 PID $pid 位于容器 cgroup，交由 Docker 扫描。"
    return 0
  fi
  exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null || echo "/proc/$pid/exe")"
  [[ -x "/proc/$pid/exe" ]] || { echo "跳过主进程 $pid：无法执行 $exe" >&2; FAIL_COUNT=$((FAIL_COUNT + 1)); return 0; }
  config_file="$TMP_DIR/host-$pid.conf"
  process_args="$(tr '\000' ' ' < "/proc/$pid/cmdline" 2>/dev/null || true)"
  process_cwd="$(readlink -f "/proc/$pid/cwd" 2>/dev/null || true)"
  [[ -n "$process_cwd" ]] || process_cwd="/proc/$pid/cwd"
  # 保留 master 启动时指定的配置文件和 prefix，确保 -T 检查的是该实例正在使用的配置。
  custom_config="$(printf '%s\n' "$process_args" | sed -n 's/.*[[:space:]]-c[[:space:]]\([^[:space:]]*\).*/\1/p')"
  custom_prefix="$(printf '%s\n' "$process_args" | sed -n 's/.*[[:space:]]-p[[:space:]]\([^[:space:]]*\).*/\1/p')"
  identity="$exe|$process_cwd|${custom_prefix:-default}|${custom_config:-default}"
  # ID 不包含 PID，master 重启后仍能更新原有拓扑关系。
  instance_id="host-$(printf '%s' "$identity" | cksum | awk '{print $1}')"
  local -a nginx_args=(-T)
  [[ -n "$custom_prefix" ]] && nginx_args+=(-p "$custom_prefix")
  [[ -n "$custom_config" ]] && nginx_args+=(-c "$custom_config")
  if ! (cd "/proc/$pid/cwd" && "$exe" "${nginx_args[@]}") > "$config_file" 2>&1; then
    echo "读取主机 Nginx 主进程 $pid 配置失败（工作目录：$process_cwd），nginx -T 输出：" >&2
    tail -n 20 "$config_file" >&2
    FAIL_COUNT=$((FAIL_COUNT + 1))
    return 0
  fi
  sed -i.bak \
    -e '/^nginx: the configuration file .* syntax is ok$/d' \
    -e '/^nginx: configuration file .* test is successful$/d' \
    "$config_file"
  rm -f "$config_file.bak"
  upload_config "$instance_id" "主机 Nginx PID $pid" "$config_file"
}

collect_container_master() {
  local container_id="$1" container_name="$2" pid config_file pids metadata exe custom_config custom_prefix identity instance_id
  pids="$(docker exec "$container_id" sh -c '
    # 在容器自己的 PID 命名空间中查找所有 Nginx master。
    for file in /proc/[0-9]*/cmdline; do
      [ -r "$file" ] || continue
      cmd=$(tr "\\000" " " < "$file" 2>/dev/null) || continue
      case "$cmd" in
        *"nginx: master process"*)
          pid=${file#/proc/}; pid=${pid%/cmdline}
          # 进程可能在扫描过程中退出；跳过已经没有可执行文件的 PID，避免误把容器 init 或陈旧 PID 当成 Nginx。
          [ -x "/proc/$pid/exe" ] || continue
          echo "$pid"
          ;;
      esac
    done
  ' 2>/dev/null | sort -un || true)"
  [[ -n "$pids" ]] || return 0
  while IFS= read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    config_file="$TMP_DIR/container-$container_id-$pid.conf"
    metadata="$(docker exec "$container_id" sh -c '
      pid="$1"
      [ -x "/proc/$pid/exe" ] || exit 3
      cmd=$(tr "\\000" " " < "/proc/$pid/cmdline" 2>/dev/null || true)
      config=$(printf "%s\\n" "$cmd" | sed -n "s/.*[[:space:]]-c[[:space:]]\\([^[:space:]]*\\).*/\\1/p")
      prefix=$(printf "%s\\n" "$cmd" | sed -n "s/.*[[:space:]]-p[[:space:]]\\([^[:space:]]*\\).*/\\1/p")
      exe=$(readlink "/proc/$pid/exe" 2>/dev/null || true)
      [ -n "$exe" ] || exe=$(command -v nginx 2>/dev/null || echo nginx)
      printf "%s|%s|%s" "$exe" "$prefix" "$config"
    ' sh "$pid" 2>/dev/null || true)"
    if [[ -z "$metadata" ]]; then
      echo "跳过容器 $container_name 中已退出或不可执行的 Nginx PID $pid" >&2
      continue
    fi
    IFS='|' read -r exe custom_prefix custom_config <<< "$metadata"
    identity="${container_name}|${exe:-nginx}|${custom_prefix:-default}|${custom_config:-default}"
    instance_id="docker-$(printf '%s' "$identity" | cksum | awk '{print $1}')"
    if ! docker exec "$container_id" sh -c '
      pid="$1"; prefix="$2"; config="$3"; exe="$4"
      if [ -x "$exe" ]; then
        set -- "$exe" -T
      elif [ -x "/proc/$pid/exe" ]; then
        set -- "/proc/$pid/exe" -T
      else
        echo "Nginx PID $pid 已退出或找不到对应的可执行文件" >&2
        exit 3
      fi
      [ -z "$prefix" ] || set -- "$@" -p "$prefix"
      [ -z "$config" ] || set -- "$@" -c "$config"
      exec "$@"
    ' sh "$pid" "${custom_prefix:-}" "${custom_config:-}" "${exe:-nginx}" > "$config_file" 2>&1; then
      echo "读取容器 $container_name 中 Nginx 主进程 $pid 配置失败，nginx -T 输出：" >&2
      tail -n 20 "$config_file" >&2
      FAIL_COUNT=$((FAIL_COUNT + 1))
      continue
    fi
    sed -i.bak \
      -e '/^nginx: the configuration file .* syntax is ok$/d' \
      -e '/^nginx: configuration file .* test is successful$/d' \
      "$config_file"
    rm -f "$config_file.bak"
    upload_config "$instance_id" "容器 $container_name Nginx PID $pid" "$config_file"
  done <<< "$pids"
}

host_pids=""
for file in /proc/[0-9]*/cmdline; do
  [[ -r "$file" ]] || continue
  process_args="$(tr '\000' ' ' < "$file" 2>/dev/null || true)"
  [[ "$process_args" == *"nginx: master process"* ]] || continue
  pid="${file#/proc/}"
  pid="${pid%/cmdline}"
  [[ "$pid" =~ ^[0-9]+$ ]] && host_pids+="$pid"$'\n'
done
host_pids="$(printf '%s' "$host_pids" | sort -un)"
if [[ -n "$host_pids" ]]; then
  while IFS= read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] && collect_host_master "$pid"
  done <<< "$host_pids"
fi

if command -v docker >/dev/null 2>&1; then
  # 扫描所有运行中的容器；无 Nginx master 的容器会被自动跳过。
  container_ids="$(docker ps -q 2>/dev/null || true)"
  if [[ -n "$container_ids" ]]; then
    while IFS= read -r container_id; do
      [[ -n "$container_id" ]] || continue
      container_name="$(docker inspect --format '{{.Name}}' "$container_id" 2>/dev/null | sed 's#^/##' || true)"
      [[ -n "$container_name" ]] || container_name="$container_id"
      if docker exec "$container_id" sh -c 'test -d /proc' >/dev/null 2>&1; then
        collect_container_master "$container_id" "$container_name"
      fi
    done <<< "$container_ids"
  fi
fi

if (( SUCCESS_COUNT == 0 )); then
  echo "没有成功获取并上传任何 Nginx 主进程配置。请确认进程可见、Docker 权限可用且 curl/base64 已安装。" >&2
  exit 1
fi
echo "采集结束：成功 $SUCCESS_COUNT 个，失败 $FAIL_COUNT 个。"
(( FAIL_COUNT == 0 ))
