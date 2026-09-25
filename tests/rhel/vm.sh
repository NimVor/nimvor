#!/usr/bin/env bash
#
# Boots an AlmaLinux cloud image under KVM for the RHEL tests (CI only):
#   tests/rhel/vm.sh boot [9|10]     start the VM and wait for SSH
#   tests/rhel/vm.sh ssh CMD...      run a command in the VM (as root)
#   tests/rhel/vm.sh put SRC DST     copy a file or directory into the VM
# SELinux stays enforcing, as on a real server.

set -Eeuo pipefail
WORK=${VM_WORK:-$PWD/.vm}
PORT=${VM_SSH_PORT:-2222}
SSH_OPTS=(-i "$WORK/key" -p "$PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=5)

case "${1:-}" in
boot)
    major=${2:-9}
    mkdir -p "$WORK"
    image=$WORK/alma.qcow2
    curl -fsSL --retry 3 -o "$image" "https://repo.almalinux.org/almalinux/${major}/cloud/x86_64/images/AlmaLinux-${major}-GenericCloud-latest.x86_64.qcow2"
    qemu-img resize "$image" 30G >/dev/null
    ssh-keygen -q -t ed25519 -N '' -f "$WORK/key"
    cat > "$WORK/user-data" <<EOF
#cloud-config
disable_root: false
users:
  - name: root
    ssh_authorized_keys: ["$(cat "$WORK/key.pub")"]
ssh_pwauth: false
growpart: {mode: auto, devices: ["/"]}
EOF
    printf 'instance-id: whacp-ci\nlocal-hostname: alma.whacp.test\n' > "$WORK/meta-data"
    cloud-localds "$WORK/seed.img" "$WORK/user-data" "$WORK/meta-data"
    qemu-system-x86_64 -enable-kvm -machine q35 -cpu host -m 6144 -smp 4 \
        -drive "file=$image,if=virtio" -drive "file=$WORK/seed.img,if=virtio,format=raw" \
        -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:${PORT}-:22" \
        -display none -serial "file:$WORK/console.log" -daemonize -pidfile "$WORK/qemu.pid"
    for _ in $(seq 1 120); do
        if ssh "${SSH_OPTS[@]}" root@127.0.0.1 true 2>/dev/null; then
            ssh "${SSH_OPTS[@]}" root@127.0.0.1 'cloud-init status --wait >/dev/null; cat /etc/os-release | head -2; getenforce'
            exit 0
        fi
        sleep 5
    done
    tail -50 "$WORK/console.log" >&2
    echo "VM did not come up" >&2
    exit 1
    ;;
ssh)
    shift
    exec ssh "${SSH_OPTS[@]}" root@127.0.0.1 "$@"
    ;;
put)
    exec scp -q -r -i "$WORK/key" -P "$PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "$2" "root@127.0.0.1:$3"
    ;;
*)
    sed -n '3,8p' "$0"
    exit 2
    ;;
esac
