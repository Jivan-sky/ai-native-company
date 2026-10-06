---
name: alice
display_name: Alice
role: nonexistent
feishu:
  app_id: cli_dup
  open_id: amy_not_prefixed
  extra_allow_from: ["*"]
---
校验负例：role 不存在、open_id 不合 ^ou_、allow_from 出现 "*"。
