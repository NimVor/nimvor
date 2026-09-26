#!/usr/bin/env bash
#
# Boots a cloud image under KVM for the real-service tests (CI only):
#   tests/vm.sh boot alma 9|10          AlmaLinux (SELinux stays enforcing)
#   tests/vm.sh boot rocky 9|10         Rocky Linux (SELinux stays enforcing)
#   tests/vm.sh boot ubuntu 22.04|26.04 Ubuntu LTS
#   tests/vm.sh ssh CMD...              run a command in the VM (as root)
#   tests/vm.sh put SRC DST             copy a file or directory into the VM
# The images are the distributions' own cloud images, as on a real server.

set -Eeuo pipefail
WORK=${VM_WORK:-$PWD/.vm}
PORT=${VM_SSH_PORT:-2222}
SSH_OPTS=(-i "$WORK/key" -p "$PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=5)

case "${1:-}" in
boot)
    os=${2:-alma}
    version=${3:-9}
    mkdir -p "$WORK"
    image=$WORK/disk.qcow2
    case $os in
    alma) url="https://repo.almalinux.org/almalinux/${version}/cloud/x86_64/images/AlmaLinux-${version}-GenericCloud-latest.x86_64.qcow2" ;;
    ubuntu) url="https://cloud-images.ubuntu.com/releases/${version}/release/ubuntu-${version}-server-cloudimg-amd64.img" ;;
    rocky) url="https://dl.rockylinux.org/pub/rocky/${version}/images/x86_64/Rocky-${version}-GenericCloud-Base.latest.x86_64.qcow2" ;;
    *) echo "unknown OS $os" >&2; exit 2 ;;
    esac
    curl -fsSL --retry 3 -o "$image" "$url"
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
    printf 'instance-id: whacp-ci\nlocal-hostname: %s.whacp.test\n' "$os" > "$WORK/meta-data"
    cloud-localds "$WORK/seed.img" "$WORK/user-data" "$WORK/meta-data"
    qemu-system-x86_64 -enable-kvm -machine q35 -cpu host -m 6144 -smp 4 \
        -drive "file=$image,if=virtio,format=qcow2" -drive "file=$WORK/seed.img,if=virtio,format=raw" \
        -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:${PORT}-:22" \
        -display none -serial "file:$WORK/console.log" -daemonize -pidfile "$WORK/qemu.pid"
    for _ in $(seq 1 120); do
        if ssh "${SSH_OPTS[@]}" root@127.0.0.1 true 2>/dev/null; then
            ssh "${SSH_OPTS[@]}" root@127.0.0.1 'cloud-init status --wait >/dev/null; grep -E "^(PRETTY_NAME|VERSION_ID)=" /etc/os-release; command -v getenforce >/dev/null && getenforce; true'
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
    sed -n '3,10p' "$0"
    exit 2
    ;;
esac
