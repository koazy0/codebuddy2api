#!/bin/bash
# 开发实例：独立端口 + 独立库 + 前端磁盘热读。
# 生产实例（8088）完全不受影响，本脚本不会去 kill 任何进程。
cd "$(dirname "$0")"

APP_NAME="cb-gateway-dev"
BIN="./${APP_NAME}"
PID_FILE="./${APP_NAME}.pid"
LOG_FILE="./dev.log"
CONFIG="./config.dev.yaml"
PORT="18088"

build() {
  echo "编译 dev 二进制 ..."
  CGO_ENABLED=1 go build -buildvcs=false -o "$BIN" .
  [ $? -ne 0 ] && { echo "编译失败"; exit 1; }
  echo "编译成功"
}

running_pid() {
  [ -f "$PID_FILE" ] || return 1
  local pid; pid=$(cat "$PID_FILE")
  if kill -0 "$pid" 2>/dev/null; then
    # 确认确实是我们的 dev 二进制，避免 pid 复用误杀/误判
    if tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null | grep -q "$APP_NAME"; then
      echo "$pid"; return 0
    fi
  fi
  return 1
}

stop() {
  local pid
  if pid=$(running_pid); then
    echo "停止 dev 实例 PID=$pid ..."
    kill "$pid"
    for _ in $(seq 1 30); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 1
    done
    kill -0 "$pid" 2>/dev/null && kill -9 "$pid"
  fi
  rm -f "$PID_FILE"
}

start() {
  local pid
  if pid=$(running_pid); then
    echo "dev 实例已在运行 PID=$pid（端口 $PORT）"
    echo "改前端只要刷新浏览器，无需重启。"
    return 0
  fi
  build
  echo "启动 dev 实例（端口 $PORT，配置 $CONFIG）..."
  # setsid + nohup：彻底脱离当前 shell 的进程组，
  # 避免调用方（终端/CI/工具）退出时把 dev 实例一起带走。
  export GATEWAY_PID_FILE="$(pwd)/${APP_NAME}.pid"
  setsid nohup "$BIN" server -c "$CONFIG" --dev > "$LOG_FILE" 2>&1 < /dev/null &
  for _ in $(seq 1 25); do
    if pid=$(running_pid); then
      echo "dev 启动成功 PID=$pid"
      echo "控制台: http://127.0.0.1:$PORT/"
      echo "日志:   tail -f $LOG_FILE"
      return 0
    fi
    sleep 0.2
  done
  echo "dev 启动失败，查看日志: cat $LOG_FILE"
  exit 1
}

status() {
  local pid
  if pid=$(running_pid); then
    echo "dev 运行中 PID=$pid  端口=$PORT"
  else
    echo "dev 未运行"
    return 1
  fi
}

case "${1:-start}" in
  start)   start   ;;
  stop)    stop    ;;
  restart) stop; start ;;
  status)  status  ;;
  build)   build   ;;
  logs)    tail -f "$LOG_FILE" ;;
  *) echo "用法: $0 {start|stop|restart|status|build|logs}" ;;
esac
