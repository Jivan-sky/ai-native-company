#!/bin/sh
# 沙箱自检：打印环境里有没有密钥、能不能写 /app、有没有网。
echo "hello from sandbox"
echo "cwd=$(pwd)"
echo "secrets_in_env=$(env | grep -ci 'key\|token\|secret' || true)"
echo -n "write_app="; (echo x > /app/t.txt) 2>/dev/null && echo yes || echo no
echo -n "write_tmp="; (echo x > /tmp/t.txt) 2>/dev/null && echo yes || echo no
if command -v wget >/dev/null 2>&1; then
  wget -q -T 2 -O /dev/null http://example.com && echo "net=reachable" || echo "net=blocked"
else
  echo "net=no-client (network=none)"
fi
