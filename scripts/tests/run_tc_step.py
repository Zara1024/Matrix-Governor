import sys

from ssh_conf import connect


def execute(cmd):
    ssh = connect()
    stdin, stdout, stderr = ssh.exec_command(cmd)
    out = stdout.read().decode('utf-8', errors='replace')
    err = stderr.read().decode('utf-8', errors='replace')
    print(">>> STDOUT:")
    print(out)
    if err:
        print(">>> STDERR:")
        print(err)
    ssh.close()
    return out


if __name__ == "__main__":
    if len(sys.argv) > 1:
        with open(sys.argv[1], 'r', encoding='utf-8') as f:
            script = f.read()
        execute(script)
