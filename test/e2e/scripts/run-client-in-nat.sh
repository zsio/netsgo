#!/bin/sh
set -eu

: "${NETSGO_NAT_WAN_IP:?NETSGO_NAT_WAN_IP is required}"
: "${NETSGO_NAT_LAN_IP:?NETSGO_NAT_LAN_IP is required}"
: "${NETSGO_NAT_CLIENT_IP:?NETSGO_NAT_CLIENT_IP is required}"

namespace="netsgo-client"
wan_if="$(ip -o -4 addr show | awk -v ip="${NETSGO_NAT_WAN_IP}" '$4 ~ ("^" ip "/") { print $2; exit }')"
if [ -z "${wan_if}" ]; then
	echo "unable to identify WAN interface for ${NETSGO_NAT_WAN_IP}" >&2
	exit 1
fi

ip netns add "${namespace}"
ip link add netsgo-lan type veth peer name netsgo-client
ip link set netsgo-client netns "${namespace}"
ip addr add "${NETSGO_NAT_LAN_IP}/24" dev netsgo-lan
ip link set netsgo-lan up
ip netns exec "${namespace}" ip link set lo up
ip netns exec "${namespace}" ip addr add "${NETSGO_NAT_CLIENT_IP}/24" dev netsgo-client
ip netns exec "${namespace}" ip link set netsgo-client up
ip netns exec "${namespace}" ip route add default via "${NETSGO_NAT_LAN_IP}"

sysctl -q -w net.ipv4.ip_forward=1

# UDP uses an endpoint-independent, stateless address mapping so simultaneous
# ICE checks cannot create conflicting conntrack translations. TCP control and
# relay traffic continue to use ordinary stateful NAT.
nft -f - <<EOF
table ip netsgo_udp_cone {
	chain raw_prerouting {
		type filter hook prerouting priority raw; policy accept;
		iifname "netsgo-lan" ip protocol udp ip saddr ${NETSGO_NAT_CLIENT_IP} notrack
		iifname "${wan_if}" ip protocol udp ip daddr ${NETSGO_NAT_WAN_IP} notrack
	}
	chain prerouting {
		type filter hook prerouting priority mangle; policy accept;
		ip protocol udp ip daddr ${NETSGO_NAT_WAN_IP} ip daddr set ${NETSGO_NAT_CLIENT_IP}
	}
	chain postrouting {
		type filter hook postrouting priority 200; policy accept;
		ip protocol udp ip saddr ${NETSGO_NAT_CLIENT_IP} ip saddr set ${NETSGO_NAT_WAN_IP}
	}
}
EOF

iptables -t nat -A POSTROUTING -s "${NETSGO_NAT_CLIENT_IP}" -o "${wan_if}" -j MASQUERADE
iptables -A FORWARD -i netsgo-lan -o "${wan_if}" -j ACCEPT
iptables -A FORWARD -i netsgo-lan -o netsgo-lan -p udp -j ACCEPT
iptables -A FORWARD -i "${wan_if}" -o netsgo-lan -p udp -d "${NETSGO_NAT_CLIENT_IP}" -j ACCEPT
iptables -A FORWARD -i "${wan_if}" -o netsgo-lan -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -P FORWARD DROP

if [ -n "${NETSGO_NAT_BACKEND_PORT:-}" ]; then
	ip netns exec "${namespace}" socat "TCP-LISTEN:${NETSGO_NAT_BACKEND_PORT},fork,reuseaddr" SYSTEM:cat &
fi
if [ -n "${NETSGO_NAT_EXPOSE_PORT:-}" ]; then
	socat "TCP-LISTEN:${NETSGO_NAT_EXPOSE_PORT},fork,reuseaddr" "TCP:${NETSGO_NAT_CLIENT_IP}:${NETSGO_NAT_EXPOSE_PORT}" &
fi

exec ip netns exec "${namespace}" /bin/sh /opt/netsgo-e2e/run-client.sh
