import paramiko
import sys

def run(cmd):
    ssh = paramiko.SSHClient()
    ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    ssh.connect("REDACTED_HOST", port=22, username="root", password="REDACTED_PASSWORD")
    stdin, stdout, stderr = ssh.exec_command(cmd)
    out = stdout.read().decode('utf-8', errors='replace')
    err = stderr.read().decode('utf-8', errors='replace')
    print("=== STDOUT ===")
    print(out)
    if err:
        print("=== STDERR ===")
        print(err)
    ssh.close()

if __name__ == "__main__":
    if len(sys.argv) > 1:
        run(" ".join(sys.argv[1:]))
    else:
        run("kubectl get nodes")
