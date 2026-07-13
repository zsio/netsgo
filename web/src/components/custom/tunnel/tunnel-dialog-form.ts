import { bpsToMbpsInput } from '@/lib/format';
import type { Client, ProxyConfig, TransportPolicy, TunnelFormType, TunnelTopology } from '@/types';

/** 编辑模式下传入的隧道数据 */
export interface TunnelDialogEditData extends ProxyConfig {
  clientId: string;
}

export interface TunnelFormState {
  name: string;
  topology: TunnelTopology;
  targetClientId: string;
  ingressClientId: string;
  bindIp: string;
  type: TunnelFormType;
  localIp: string;
  localPort: string;
  remotePort: string;
  domain: string;
  ingressBps: string;
  egressBps: string;
  totalBps: string;
  transportPolicy: TransportPolicy;
  sourceCidrs: string;
  socks5AuthEnabled: boolean;
  socks5Username: string;
  socks5Password: string;
  httpAuthEnabled: boolean;
  httpUsername: string;
  httpPassword: string;
  socks5TargetCidrs: string;
  socks5TargetHosts: string;
  socks5TargetPorts: string;
  socks5DialTimeout: string;
  confirmNoAuthRisk: boolean;
}

type TunnelInitialFormProps =
  | {
    mode: 'create';
    clientId: string;
    clients?: Client[];
  }
  | {
    mode: 'edit';
    tunnel: TunnelDialogEditData | null;
    clients?: Client[];
  };

export function getInitialTunnelFormState(props: TunnelInitialFormProps): TunnelFormState {
  if (props.mode === 'edit' && props.tunnel) {
    return {
      name: props.tunnel.name,
      topology: props.tunnel.topology ?? 'server_expose',
      targetClientId: props.tunnel.target?.client_id ?? props.tunnel.owner_client_id ?? props.tunnel.client_id ?? props.tunnel.clientId,
      ingressClientId: props.tunnel.ingress?.client_id ?? '',
      bindIp: props.tunnel.ingress?.type === 'tcp_listen' || props.tunnel.ingress?.type === 'udp_listen' || props.tunnel.ingress?.type === 'socks5_listen'
        ? props.tunnel.ingress.config.bind_ip
        : '0.0.0.0',
      type: getInitialType(props.tunnel),
      localIp: getInitialTargetHost(props.tunnel),
      localPort: String(getInitialTargetPort(props.tunnel) || ''),
      remotePort: String(getInitialIngressPort(props.tunnel) || ''),
      domain: props.tunnel.domain || '',
      ingressBps: bpsToMbpsInput(props.tunnel.ingress_bps),
      egressBps: bpsToMbpsInput(props.tunnel.egress_bps),
      totalBps: bpsToMbpsInput(props.tunnel.total_bps ?? props.tunnel.bandwidth_settings?.total_bps ?? 0),
      transportPolicy: props.tunnel.transport_policy ?? 'server_relay_only',
      sourceCidrs: getInitialSourceCIDRs(props.tunnel),
      socks5AuthEnabled: props.tunnel.ingress?.type === 'socks5_listen'
        ? props.tunnel.ingress.config.auth.type === 'username_password'
        : false,
      socks5Username: props.tunnel.ingress?.type === 'socks5_listen'
        ? props.tunnel.ingress.config.auth.username ?? ''
        : '',
      socks5Password: '',
      httpAuthEnabled: props.tunnel.ingress?.type === 'http_host'
        ? props.tunnel.ingress.config.auth?.type === 'basic'
        : false,
      httpUsername: props.tunnel.ingress?.type === 'http_host'
        ? props.tunnel.ingress.config.auth?.username ?? ''
        : '',
      httpPassword: '',
      socks5TargetCidrs: props.tunnel.target?.type === 'socks5_connect_handler'
        ? props.tunnel.target.config.allowed_target_cidrs.join(', ')
        : '0.0.0.0/0, ::/0',
      socks5TargetHosts: props.tunnel.target?.type === 'socks5_connect_handler'
        ? props.tunnel.target.config.allowed_target_hosts.join(', ')
        : '',
      socks5TargetPorts: props.tunnel.target?.type === 'socks5_connect_handler'
        ? props.tunnel.target.config.allowed_target_ports.join(', ')
        : '',
      socks5DialTimeout: props.tunnel.target?.type === 'socks5_connect_handler'
        ? String(props.tunnel.target.config.dial_timeout_seconds || 10)
        : '10',
      confirmNoAuthRisk: false,
    };
  }

  return {
    name: '',
    topology: 'server_expose',
    targetClientId: props.mode === 'create' ? props.clientId : '',
    ingressClientId: '',
    bindIp: '0.0.0.0',
    type: 'tcp',
    localIp: '127.0.0.1',
    localPort: '',
    remotePort: '',
    domain: '',
    ingressBps: '',
    egressBps: '',
    totalBps: '',
    transportPolicy: 'server_relay_only',
    sourceCidrs: '0.0.0.0/0, ::/0',
    socks5AuthEnabled: false,
    socks5Username: '',
    socks5Password: '',
    httpAuthEnabled: false,
    httpUsername: '',
    httpPassword: '',
    socks5TargetCidrs: '0.0.0.0/0, ::/0',
    socks5TargetHosts: '',
    socks5TargetPorts: '',
    socks5DialTimeout: '10',
    confirmNoAuthRisk: false,
  };
}

function getInitialIngressPort(tunnel: TunnelDialogEditData) {
  if (tunnel.ingress?.type === 'tcp_listen' || tunnel.ingress?.type === 'udp_listen' || tunnel.ingress?.type === 'socks5_listen') {
    return tunnel.ingress.config.port;
  }
  return tunnel.remote_port;
}

function getInitialType(tunnel: TunnelDialogEditData): TunnelFormType {
  if (tunnel.ingress?.type === 'socks5_listen' || tunnel.target?.type === 'socks5_connect_handler') {
    return 'socks5';
  }
  return tunnel.type;
}

function getInitialSourceCIDRs(tunnel: TunnelDialogEditData) {
  const ingress = tunnel.ingress;
  if (
    ingress?.type === 'tcp_listen'
    || ingress?.type === 'udp_listen'
    || ingress?.type === 'http_host'
    || ingress?.type === 'socks5_listen'
  ) {
    const cidrs = ingress.config.allowed_source_cidrs ?? [];
    if (cidrs.length > 0) {
      return cidrs.join(', ');
    }
  }
  return '0.0.0.0/0, ::/0';
}

function getInitialTargetHost(tunnel: TunnelDialogEditData) {
  if (tunnel.target?.type === 'tcp_service' || tunnel.target?.type === 'udp_service') {
    return tunnel.target.config.ip || tunnel.target.config.host || '127.0.0.1';
  }
  return tunnel.local_ip || '127.0.0.1';
}

function getInitialTargetPort(tunnel: TunnelDialogEditData) {
  if (tunnel.target?.type === 'tcp_service' || tunnel.target?.type === 'udp_service') {
    return tunnel.target.config.port;
  }
  return tunnel.local_port;
}
