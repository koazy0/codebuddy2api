#!/usr/bin/env bash
# 提交前扫一遍仓库，防止把真实 key / 账密 / 本地配置推上去。
# 用法：./scripts/check-secrets.sh
set -uo pipefail
cd "$(dirname "$0")/.."

fail=0

# 1) 有些文件永远不该进版本库。
for path in config.yaml config.dev.yaml .env data log run.log codebuddy-gateway cb-gateway-dev; do
  if git ls-files --error-unmatch "$path" >/dev/null 2>&1; then
    echo "✗ 不应提交：$path（已加入 .gitignore，请 git rm --cached $path）"
    fail=1
  fi
done

# 2) 真实凭证的形态：JWT、sk- 开头的长 key、控制台密码摘要以外的明文密码。
patterns=(
  'eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}'
  'sk-[A-Za-z0-9]{16,}'
  'AKIA[0-9A-Z]{16}'
  'BEGIN [A-Z ]*PRIVATE KEY'
)
for pat in "${patterns[@]}"; do
  hits=$(git grep -nIE "$pat" -- . ':(exclude)*_test.go' || true)
  if [ -n "$hits" ]; then
    echo "✗ 疑似凭证："
    echo "$hits"
    fail=1
  fi
done

# 3) 手机号这类账号标识，除了测试夹具外不该出现。
hits=$(git grep -nIE '\b1[3-9][0-9]{9}\b' -- . ':(exclude)*_test.go' || true)
if [ -n "$hits" ]; then
  echo "✗ 疑似真实手机号："
  echo "$hits"
  fail=1
fi

# 4) 配置里的密码必须是空的或 sha256 摘要。
hits=$(git grep -nIE '^\s*password: *["'"'"']?[^"'"'"' ]' -- '*config*.yaml*' || true)
if [ -n "$hits" ]; then
  echo "✗ 配置文件里出现了明文密码："
  echo "$hits"
  fail=1
fi

if [ "$fail" -eq 0 ]; then
  echo "✓ 未发现需要脱敏的内容"
fi
exit "$fail"
