"""测试机 SSH 连接配置。

主机地址与口令一律从环境变量读取，禁止在仓库中硬编码。

使用前先 export（Windows PowerShell 用 $env:MATRIX_TEST_HOST="..."）：

    export MATRIX_TEST_HOST=1.2.3.4
    export MATRIX_TEST_USER=root
    export MATRIX_TEST_KEY=~/.ssh/id_ed25519     # 推荐：密钥登录
    # 或（不推荐，仅临时环境）
    export MATRIX_TEST_PASSWORD=...

可选：

    export MATRIX_TEST_PORT=22
"""

import os
import sys

import paramiko


def _require(name):
    value = os.environ.get(name)
    if not value:
        sys.exit(
            "缺少环境变量 %s。请先参考 scripts/tests/ssh_conf.py 顶部的说明完成 export。"
            % name
        )
    return value


def connect():
    """按环境变量建立到测试机的 SSH 连接。"""
    host = _require("MATRIX_TEST_HOST")
    port = int(os.environ.get("MATRIX_TEST_PORT", "22"))
    user = os.environ.get("MATRIX_TEST_USER", "root")

    ssh = paramiko.SSHClient()
    ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())

    key_path = os.environ.get("MATRIX_TEST_KEY")
    if key_path:
        ssh.connect(
            host,
            port=port,
            username=user,
            key_filename=os.path.expanduser(key_path),
        )
    else:
        password = _require("MATRIX_TEST_PASSWORD")
        ssh.connect(host, port=port, username=user, password=password)

    return ssh
