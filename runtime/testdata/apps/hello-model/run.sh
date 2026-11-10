#!/bin/sh
# 应用手里只有：socket 路径 + 一张短时票。没有密钥、没有端点、没有网络。
echo "问 ANC…"
anc model ask --prompt "用一句话说明：为什么应用不该自己拿密钥"
