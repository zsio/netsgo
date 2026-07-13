import { useState, type ComponentType } from 'react';
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter, DialogTrigger,
} from '@/components/ui/dialog';
import {
  AlertTriangle, Cable, ChevronDown, Globe, GitBranchPlus,
  Radio, Waypoints,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Checkbox } from '@/components/ui/checkbox';
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from '@/components/ui/input-group';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import toast from 'react-hot-toast';
import { useCreateTunnel, useUpdateTunnel } from '@/hooks/use-tunnel-mutations';
import {
  getTunnelMutationErrorMessage,
  getTunnelMutationFieldError,
} from '@/lib/tunnel-model';
import { parseMbpsInputToBps } from '@/lib/format';
import { useServerStatus } from '@/hooks/use-server-status';
import { getClientDisplayName } from '@/lib/client-utils';
import { cn } from '@/lib/utils';
import type { Client, PortRange, TransportPolicy, TunnelFormType, TunnelTopology } from '@/types';
import { i18n } from '@/i18n';
import { useTranslation } from 'react-i18next';
import { getInitialTunnelFormState, type TunnelDialogEditData } from './tunnel-dialog-form';
import {
  getDefaultSourceCidrs,
  isDefaultSourceCidrs,
  preserveLoopbackSourceCIDRsOnFirstRestriction,
  shouldWarnMissingLoopbackSourceCIDRs,
} from '@/lib/source-cidrs';

interface TunnelDialogCreateProps {
  mode: 'create';
  clientId: string;
  clients?: Client[];
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  /** 触发按钮（作为 DialogTrigger children） */
  trigger?: React.ReactNode;
  hideTrigger?: boolean;
}

interface TunnelDialogEditProps {
  mode: 'edit';
  tunnel: TunnelDialogEditData | null;
  clients?: Client[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type TunnelDialogProps = TunnelDialogCreateProps | TunnelDialogEditProps;

type IconType = ComponentType<{ className?: string }>;

const typeOptions: { value: TunnelFormType; label: string; icon: IconType; descKey: string }[] = [
  { value: 'tcp', label: 'TCP', icon: Cable, descKey: 'tunnels.protocolDescTcp' },
  { value: 'udp', label: 'UDP', icon: Radio, descKey: 'tunnels.protocolDescUdp' },
  { value: 'http', label: 'HTTP', icon: Globe, descKey: 'tunnels.protocolDescHttp' },
  { value: 'socks5', label: 'SOCKS5', icon: Waypoints, descKey: 'tunnels.protocolDescSocks5' },
];

interface LocalFieldError {
  field: string;
  message: string;
  code?: string;
  source?: 'local' | 'server';
}

function fieldErrorMatches(error: LocalFieldError | null, fields: readonly string[]) {
  return Boolean(error && fields.includes(error.field));
}

function FieldErrorText({
  error,
  fields,
}: {
  error: LocalFieldError | null;
  fields: readonly string[];
}) {
  if (!fieldErrorMatches(error, fields)) {
    return null;
  }
  return (
    <p className="text-[11px] font-medium text-destructive">
      {error?.message}
    </p>
  );
}

function getFormKey(props: TunnelDialogProps, open: boolean) {
  if (props.mode === 'edit') {
    const tunnelKey = props.tunnel
      ? `${props.tunnel.clientId}:${props.tunnel.id}`
      : 'empty';
    return `edit:${tunnelKey}:${open ? 'open' : 'closed'}`;
  }

  return `create:${props.clientId}:${open ? 'open' : 'closed'}`;
}

function isPortAllowedByRanges(port: number, ranges: PortRange[] | undefined) {
  if (!ranges || ranges.length === 0) {
    return true;
  }
  return ranges.some((range) => port >= range.start && port <= range.end);
}

function parsePortInput(value: string) {
  if (!/^\d+$/.test(value.trim())) {
    return null;
  }
  const port = Number.parseInt(value, 10);
  return port >= 1 && port <= 65535 ? port : null;
}

function parseCommaSeparatedList(value: string) {
  return value.split(',').map((item) => item.trim()).filter(Boolean);
}

function parseSourceCIDRInput(value: string) {
  const parsed = parseCommaSeparatedList(value);
  return parsed.length > 0 ? parsed : ['0.0.0.0/0', '::/0'];
}

function parseCommaSeparatedPortList(value: string) {
  const ports: number[] = [];
  for (const item of parseCommaSeparatedList(value)) {
    const port = parsePortInput(item);
    if (port === null) {
      return null;
    }
    ports.push(port);
  }
  return ports;
}

function localFieldError(field: string, message: string): LocalFieldError {
  return { field, message, code: 'invalid_field', source: 'local' };
}

function serverFieldError(error: unknown): LocalFieldError | null {
  return getTunnelMutationFieldError(error);
}

const ADVANCED_FIELD_NAMES = new Set<string>([
  'ingress.config.allowed_source_cidrs',
  'ingress.config',
  'ingress.config.auth',
  'ingress.config.auth.username',
  'ingress.config.auth.password',
  'confirm_no_auth_risk',
  'target.config.dial_timeout_seconds',
  'target.config.allowed_target_cidrs',
  'target.config.allowed_target_hosts',
  'target.config.allowed_target_ports',
  'target.config',
  'ingress_bps',
  'bandwidth_settings.ingress_bps',
  'egress_bps',
  'bandwidth_settings.egress_bps',
  'total_bps',
  'bandwidth_settings.total_bps',
]);

function isAdvancedFieldError(field: string | undefined) {
  return Boolean(field && ADVANCED_FIELD_NAMES.has(field));
}

function formatPortRanges(ranges: PortRange[] | undefined) {
  if (!ranges || ranges.length === 0) {
    return i18n.t('tunnels.unrestricted');
  }
  return ranges.map((range) => range.start === range.end ? range.start : `${range.start}-${range.end}`).join(', ');
}

function topologyCardClassName(selected: boolean) {
  return cn(
    'flex w-full flex-col items-start gap-0.5 rounded-lg border px-3 py-2 text-left transition-colors',
    'focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
    selected
      ? 'border-primary bg-primary/10'
      : 'border-input bg-background hover:bg-muted/50',
    'disabled:opacity-60',
  );
}

function protocolCardClassName(selected: boolean) {
  return cn(
    'flex flex-1 items-center justify-center gap-1.5 rounded-lg border px-3 py-2 text-sm font-medium transition-colors',
    'focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
    selected
      ? 'border-primary bg-primary/10 text-primary'
      : 'border-input bg-background text-foreground hover:bg-muted/50',
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <p className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
      {children}
    </p>
  );
}

export function TopologyCardButton({
  selected,
  disabled,
  label,
  description,
  onSelect,
}: {
  selected: boolean;
  disabled?: boolean;
  label: string;
  description?: string;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onSelect}
      className={topologyCardClassName(selected)}
    >
      <span className="text-sm font-medium">{label}</span>
      {description ? (
        <span className="text-[11px] leading-snug text-muted-foreground">{description}</span>
      ) : null}
    </button>
  );
}

export function ClientToClientTopologyButton({
  selected,
  disabled,
  label,
  description,
  tooltip,
  onSelect,
}: {
  selected: boolean;
  disabled: boolean;
  label: string;
  description?: string;
  tooltip: string;
  onSelect: () => void;
}) {
  const card = (
    <TopologyCardButton
      selected={selected}
      disabled={disabled}
      label={label}
      description={description}
      onSelect={onSelect}
    />
  );

  if (!disabled) {
    return card;
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="block cursor-not-allowed" tabIndex={0}>
          {card}
        </span>
      </TooltipTrigger>
      <TooltipContent side="top">
        {tooltip}
      </TooltipContent>
    </Tooltip>
  );
}

export function TunnelDialog(props: TunnelDialogProps) {
  const { t } = useTranslation();
  const isEdit = props.mode === 'edit';

  // --- 弹窗开关 ---
  const [internalOpen, setInternalOpen] = useState(false);
  const open = isEdit ? props.open : (props.open ?? internalOpen);
  const setOpen = isEdit
    ? props.onOpenChange
    : (props.onOpenChange ?? setInternalOpen);

  const formKey = getFormKey(props, open);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {!isEdit && !props.hideTrigger && (
        <DialogTrigger asChild>
          {(props as TunnelDialogCreateProps).trigger ?? (
            <Button>
              <GitBranchPlus className="h-4 w-4 mr-1.5" />
              {t('tunnels.addTunnel')}
            </Button>
          )}
        </DialogTrigger>
      )}
      <TunnelDialogForm
        key={formKey}
        props={props}
        open={open}
        setOpen={setOpen}
      />
    </Dialog>
  );
}

function TunnelDialogForm({
  props,
  open,
  setOpen,
}: {
  props: TunnelDialogProps;
  open: boolean;
  setOpen: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const isEdit = props.mode === 'edit';
  const initialForm = getInitialTunnelFormState(props);
  const [name, setName] = useState(initialForm.name);
  const [topology, setTopology] = useState<TunnelTopology>(initialForm.topology);
  const [targetClientId, setTargetClientId] = useState(initialForm.targetClientId);
  const [ingressClientId, setIngressClientId] = useState(initialForm.ingressClientId);
  const [bindIp, setBindIp] = useState(initialForm.bindIp);
  const [type, setType] = useState<TunnelFormType>(initialForm.type);
  const [localIp, setLocalIp] = useState(initialForm.localIp);
  const [localPort, setLocalPort] = useState(initialForm.localPort);
  const [remotePort, setRemotePort] = useState(initialForm.remotePort);
  const [domain, setDomain] = useState(initialForm.domain);
  const [ingressBps, setIngressBps] = useState(initialForm.ingressBps);
  const [egressBps, setEgressBps] = useState(initialForm.egressBps);
  const [totalBps, setTotalBps] = useState(initialForm.totalBps);
  const [transportPolicy, setTransportPolicy] = useState<TransportPolicy>(initialForm.transportPolicy);
  const [fieldError, setFieldError] = useState<LocalFieldError | null>(null);
  const [sourceCidrs, setSourceCidrs] = useState(initialForm.sourceCidrs);
  const [socks5AuthEnabled, setSocks5AuthEnabled] = useState(initialForm.socks5AuthEnabled);
  const [socks5Username, setSocks5Username] = useState(initialForm.socks5Username);
  const [socks5Password, setSocks5Password] = useState(initialForm.socks5Password);
  const [httpAuthEnabled, setHttpAuthEnabled] = useState(initialForm.httpAuthEnabled);
  const [httpUsername, setHttpUsername] = useState(initialForm.httpUsername);
  const [httpPassword, setHttpPassword] = useState(initialForm.httpPassword);
  const [socks5TargetCidrs, setSocks5TargetCidrs] = useState(initialForm.socks5TargetCidrs);
  const [socks5TargetHosts, setSocks5TargetHosts] = useState(initialForm.socks5TargetHosts);
  const [socks5TargetPorts, setSocks5TargetPorts] = useState(initialForm.socks5TargetPorts);
  const socks5DialTimeout = initialForm.socks5DialTimeout;
  const [confirmNoAuthRisk, setConfirmNoAuthRisk] = useState(initialForm.confirmNoAuthRisk);
  const [advancedOpen, setAdvancedOpen] = useState(false);

  const clients = props.clients ?? [];
  const selectedTargetClientId = targetClientId || (props.mode === 'create' ? props.clientId : props.tunnel?.target?.client_id ?? props.tunnel?.owner_client_id ?? props.tunnel?.clientId ?? '');
  const sourceClient = clients.find((client) => client.id === selectedTargetClientId);
  const ingressClientOptions = clients.filter((client) => client.id !== selectedTargetClientId);
  const selectedIngressClientId = ingressClientId && ingressClientId !== selectedTargetClientId
    ? ingressClientId
    : ingressClientOptions[0]?.id || '';
  const isClientToClient = topology === 'client_to_client';
  const isHttp = type === 'http';
  const isSocks5 = type === 'socks5';
  const showLoopbackCIDRWarning = shouldWarnMissingLoopbackSourceCIDRs(sourceCidrs);
  const isEditing = props.mode === 'edit';
  const canUseClientToClient = ingressClientOptions.length > 0;
  const parsedLocalPort = parsePortInput(localPort);
  const parsedRemotePort = isHttp ? 0 : parsePortInput(remotePort);
  const parsedSocks5DialTimeout = Number.parseInt(socks5DialTimeout, 10);

  const createTunnel = useCreateTunnel();
  const updateTunnel = useUpdateTunnel();
  const mutation = isEdit ? updateTunnel : createTunnel;
  const portErrorMessage = t('tunnels.portInvalid');

  const clearMutationFeedback = () => {
    if (fieldError) {
      setFieldError(null);
    }
    if (mutation.isError) {
      mutation.reset();
    }
  };

  const failField = (error: LocalFieldError) => {
    setFieldError(error);
    if (isAdvancedFieldError(error.field)) {
      setAdvancedOpen(true);
    }
  };

  const { data: status } = useServerStatus({
    enabled: open,
    refetchOnMount: 'always',
    staleTime: 0,
  });

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setFieldError(null);
    const parsedIngressBps = parseMbpsInputToBps(ingressBps);
    const parsedEgressBps = parseMbpsInputToBps(egressBps);
    const parsedTotalBps = parseMbpsInputToBps(totalBps);

    if (!name.trim()) {
      failField(localFieldError('name', t('tunnels.nameRequired')));
      return;
    }

    if (!isSocks5 && !parsedLocalPort) {
      failField(localFieldError('local_port', portErrorMessage));
      return;
    }

    if (isHttp && !domain.trim()) {
      failField(localFieldError('domain', t('tunnels.domainRequired')));
      return;
    }

    if (!isHttp && !parsedRemotePort) {
      failField(localFieldError('remote_port', portErrorMessage));
      return;
    }

    if (isClientToClient && !canUseClientToClient) {
      failField(localFieldError('ingress.client_id', t('tunnels.c2cRequiresTwoClients')));
      return;
    }

    if (isClientToClient && !bindIp.trim()) {
      failField(localFieldError('bind_ip', t('tunnels.bindAddressRequired')));
      return;
    }

    if (parsedIngressBps == null || parsedEgressBps == null || parsedTotalBps == null) {
      failField(localFieldError(parsedIngressBps == null ? 'ingress_bps' : parsedEgressBps == null ? 'egress_bps' : 'total_bps', t('tunnels.bandwidthNonNegative')));
      return;
    }

    const allowedSourceCIDRs = parseSourceCIDRInput(sourceCidrs);
    const socks5AllowedTargetPorts = parseCommaSeparatedPortList(socks5TargetPorts);

    if (isSocks5) {
      const effectiveSocks5AuthType = socks5AuthEnabled ? 'username_password' : 'none';
      if (effectiveSocks5AuthType === 'username_password' && (!socks5Username.trim() || (!isEditing && !socks5Password))) {
        failField(localFieldError('ingress.config.auth', t('tunnels.socks5AuthRequired')));
        return;
      }
      if (!isClientToClient && effectiveSocks5AuthType === 'none' && !confirmNoAuthRisk) {
        failField(localFieldError('confirm_no_auth_risk', t('tunnels.socks5NoAuthRequired')));
        return;
      }
      if (!Number.isInteger(parsedSocks5DialTimeout) || parsedSocks5DialTimeout < 1 || parsedSocks5DialTimeout > 120) {
        failField(localFieldError('target.config.dial_timeout_seconds', t('tunnels.socks5DialTimeoutInvalid')));
        return;
      }
      if (socks5AllowedTargetPorts === null) {
        failField(localFieldError('target.config.allowed_target_ports', portErrorMessage));
        return;
      }
    }
    if (isHttp && httpAuthEnabled && (!httpUsername.trim() || (!isEditing && !httpPassword))) {
      failField(localFieldError('ingress.config.auth', t('tunnels.httpAuthRequired')));
      return;
    }

    if (!isClientToClient && !isHttp && parsedRemotePort && !isPortAllowedByRanges(parsedRemotePort, status?.allowed_ports)) {
      const message = t('tunnels.portMustBeAllowed', { ranges: formatPortRanges(status?.allowed_ports) });
      failField({ field: 'remote_port', message, code: 'port_not_allowed' });
      toast.error(message);
      return;
    }

    if (props.mode === 'edit') {
      const tunnel = props.tunnel;
      if (!tunnel) return;

      updateTunnel.mutate(
        {
          clientId: tunnel.owner_client_id ?? tunnel.client_id ?? tunnel.clientId,
          tunnelId: tunnel.id,
          expected_revision: tunnel.revision,
          topology,
          ingress_client_id: isClientToClient ? selectedIngressClientId : undefined,
          bind_ip: isClientToClient
            ? bindIp
            : undefined,
          name,
          type,
          local_ip: isSocks5 ? '' : localIp,
          local_port: parsedLocalPort ?? 0,
          remote_port: parsedRemotePort ?? 0,
          domain,
          allowed_source_cidrs: allowedSourceCIDRs,
          ingress_bps: parsedIngressBps,
          egress_bps: parsedEgressBps,
          total_bps: parsedTotalBps,
          transport_policy: isClientToClient ? transportPolicy : 'server_relay_only',
          http_auth: isHttp ? {
            enabled: httpAuthEnabled,
            username: httpUsername,
            password: httpPassword,
          } : undefined,
          socks5: isSocks5 ? {
            auth_type: socks5AuthEnabled ? 'username_password' : 'none',
            username: socks5Username,
            password: socks5Password,
            allowed_target_cidrs: parseCommaSeparatedList(socks5TargetCidrs),
            allowed_target_hosts: parseCommaSeparatedList(socks5TargetHosts),
            allowed_target_ports: socks5AllowedTargetPorts ?? [],
            dial_timeout_seconds: parsedSocks5DialTimeout,
          } : undefined,
          confirm_no_auth_risk: isSocks5 ? confirmNoAuthRisk : undefined,
        },
        {
          onSuccess: () => {
            setFieldError(null);
            setOpen(false);
            toast.success(t('tunnels.updated', { name }));
          },
          onError: (err) => {
            setFieldError(serverFieldError(err));
            toast.error(getTunnelMutationErrorMessage(err));
          },
        },
      );
      return;
    }

    createTunnel.mutate(
      {
        clientId: selectedTargetClientId,
        topology,
        ingress_client_id: isClientToClient ? selectedIngressClientId : undefined,
        bind_ip: isClientToClient ? bindIp : undefined,
        name,
        type,
        local_ip: isSocks5 ? '' : localIp,
        local_port: parsedLocalPort ?? 0,
        remote_port: parsedRemotePort ?? 0,
        domain,
        allowed_source_cidrs: allowedSourceCIDRs,
        ingress_bps: parsedIngressBps,
        egress_bps: parsedEgressBps,
        total_bps: parsedTotalBps,
        transport_policy: isClientToClient ? transportPolicy : 'server_relay_only',
        http_auth: isHttp ? {
          enabled: httpAuthEnabled,
          username: httpUsername,
          password: httpPassword,
        } : undefined,
        socks5: isSocks5 ? {
          auth_type: socks5AuthEnabled ? 'username_password' : 'none',
          username: socks5Username,
          password: socks5Password,
          allowed_target_cidrs: parseCommaSeparatedList(socks5TargetCidrs),
          allowed_target_hosts: parseCommaSeparatedList(socks5TargetHosts),
          allowed_target_ports: socks5AllowedTargetPorts ?? [],
          dial_timeout_seconds: parsedSocks5DialTimeout,
        } : undefined,
        confirm_no_auth_risk: isSocks5 ? confirmNoAuthRisk : undefined,
      },
      {
        onSuccess: () => {
          setFieldError(null);
          setOpen(false);
          toast.success(t('tunnels.created', { name }));
        },
        onError: (err) => {
          const serverError = serverFieldError(err);
          if (serverError) {
            failField(serverError);
          } else {
            setFieldError(null);
          }
          toast.error(getTunnelMutationErrorMessage(err));
        },
      },
    );
  };

  const effectiveTypeOptions = isClientToClient
    ? typeOptions.filter((opt) => opt.value !== 'http')
    : typeOptions;

  const suggestedName = (() => {
    if (name.trim()) return '';
    const portPart = (isSocks5 ? remotePort : localPort) || remotePort;
    return portPart ? `${type}-${portPart}` : '';
  })();
  const suggestedRemotePort = (!remotePort && !isHttp && !isClientToClient && status?.allowed_ports?.[0])
    ? String(status.allowed_ports[0].start)
    : '';

  const validateRemotePortRange = () => {
    const port = parsePortInput(remotePort);
    if (!isClientToClient && !isHttp && port && !isPortAllowedByRanges(port, status?.allowed_ports)) {
      setFieldError({
        field: 'remote_port',
        message: t('tunnels.portMustBeAllowed', { ranges: formatPortRanges(status?.allowed_ports) }),
        code: 'port_not_allowed',
      });
    }
  };

  return (
    <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-2xl">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <GitBranchPlus className="h-5 w-5 text-primary" />
          {isEdit ? t('tunnels.editTitle') : t('tunnels.createTitle')}
        </DialogTitle>
        {props.mode === 'edit' && (
          <DialogDescription>
            {t('tunnels.editDescription', { name: props.tunnel?.name ?? '' })}
          </DialogDescription>
        )}
      </DialogHeader>

      <form noValidate onSubmit={handleSubmit} className="flex min-h-0 flex-1 flex-col gap-4">
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto -mr-4 pr-4 pl-0.5">
        {/* 隧道名称 */}
        <div className="space-y-1.5">
          <label className="text-sm font-medium">{t('tunnels.name')}</label>
          <Input
            aria-label={t('tunnels.name')}
            placeholder={t('tunnels.namePlaceholder')}
            value={name}
            onChange={(e) => {
              clearMutationFeedback();
              setName(e.target.value);
            }}
            autoFocus
          />
          {suggestedName && (
            <button
              type="button"
              className="text-[11px] font-medium text-primary hover:underline"
              onClick={() => {
                clearMutationFeedback();
                setName(suggestedName);
              }}
            >
              {t('tunnels.suggestionPrefix')}：{suggestedName}
            </button>
          )}
          <FieldErrorText error={fieldError} fields={['name']} />
        </div>

        {/* 链路：拓扑 + 客户端 */}
        <div className="space-y-3 rounded-lg border border-border/60 bg-muted/10 p-3">
          <SectionLabel>{t('tunnels.sectionRouting')}</SectionLabel>
          {/* 隧道拓扑 */}
          <div className="space-y-1.5">
            <label className="text-sm font-medium">{t('tunnels.topology')}</label>
            <div className="grid grid-cols-2 gap-2">
              <TopologyCardButton
                selected={topology === 'server_expose'}
                label={t('tunnels.serverExpose')}
                description={t('tunnels.serverExposeDesc')}
                onSelect={() => {
                  clearMutationFeedback();
                  setTopology('server_expose');
                  if (isSocks5 && isDefaultSourceCidrs(sourceCidrs)) {
                    setSourceCidrs(getDefaultSourceCidrs(type, 'server_expose'));
                  }
                }}
              />
              <ClientToClientTopologyButton
                selected={topology === 'client_to_client'}
                disabled={!canUseClientToClient}
                label={t('tunnels.clientToClient')}
                description={t('tunnels.clientToClientDesc')}
                tooltip={t('tunnels.c2cRequiresTwoClients')}
                onSelect={() => {
                  if (!canUseClientToClient) {
                    return;
                  }
                  clearMutationFeedback();
                  setTopology('client_to_client');
                  if (type === 'http') setType('tcp');
                  if (isSocks5 && isDefaultSourceCidrs(sourceCidrs)) {
                    setSourceCidrs(getDefaultSourceCidrs(type, 'client_to_client'));
                  }
                }}
              />
            </div>
            <FieldErrorText error={fieldError} fields={['topology', 'transport_policy']} />
          </div>

          {isClientToClient && (
            <div className="space-y-1.5">
              <label className="text-sm font-medium">{t('tunnels.transportPolicy')}</label>
              <Select value={transportPolicy} onValueChange={(value) => { clearMutationFeedback(); setTransportPolicy(value as TransportPolicy); }}>
                <SelectTrigger aria-label={t('tunnels.transportPolicy')} className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="server_relay_only">{t('tunnels.serverRelay')}</SelectItem>
                  <SelectItem value="direct_preferred">{t('tunnels.directPreferred')}</SelectItem>
                  <SelectItem value="direct_only">{t('tunnels.directOnly')}</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">{t('tunnels.transportPolicyHint')}</p>
              <FieldErrorText error={fieldError} fields={['transport_policy']} />
            </div>
          )}

          {(isClientToClient || clients.length > 1) && (
            <div className={cn('grid gap-3', isClientToClient ? 'grid-cols-2' : 'grid-cols-1')}>
              <div className="space-y-1.5">
                <label className="text-sm font-medium">{t('tunnels.sourceClient')}</label>
                {clients.length > 0 ? (
                  <Select
                    value={selectedTargetClientId}
                    disabled={isEdit}
                    onValueChange={(nextTargetClientId) => {
                      clearMutationFeedback();
                      setTargetClientId(nextTargetClientId);
                      if (ingressClientId === nextTargetClientId) {
                        setIngressClientId('');
                      }
                    }}
                  >
                    <SelectTrigger aria-label={t('tunnels.sourceClient')} className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {clients.map((client) => (
                        <SelectItem key={client.id} value={client.id}>
                          {getClientDisplayName(client)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                ) : (
                  <Input value={sourceClient ? getClientDisplayName(sourceClient) : selectedTargetClientId} disabled />
                )}
                <FieldErrorText error={fieldError} fields={['target.client_id', 'client_id']} />
              </div>
              {isClientToClient && (
                <div className="space-y-1.5">
                  <label className="text-sm font-medium">{t('tunnels.ingressClient')}</label>
                  <Select
                    value={selectedIngressClientId}
                    onValueChange={(nextIngressClientId) => {
                      clearMutationFeedback();
                      setIngressClientId(nextIngressClientId);
                    }}
                  >
                    <SelectTrigger aria-label={t('tunnels.ingressClient')} className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {ingressClientOptions.map((client) => (
                        <SelectItem key={client.id} value={client.id}>
                          {getClientDisplayName(client)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <FieldErrorText error={fieldError} fields={['ingress.client_id']} />
                </div>
              )}
            </div>
          )}
        </div>

        {/* 端点与协议 */}
        <div className="space-y-3 rounded-lg border border-border/60 bg-muted/10 p-3">
          <SectionLabel>{t('tunnels.sectionEndpoint')}</SectionLabel>
          <div className="space-y-1.5">
            <label className="text-sm font-medium">{t('tunnels.protocolType')}</label>
            <div className="flex gap-2">
              {effectiveTypeOptions.map((opt) => {
                const Icon = opt.icon;
                const selected = type === opt.value;
                return (
                  <button
                    key={opt.value}
                    type="button"
                    aria-pressed={selected}
                    className={protocolCardClassName(selected)}
                    onClick={() => {
                      clearMutationFeedback();
                      setType(opt.value);
                      if (isDefaultSourceCidrs(sourceCidrs)) {
                        setSourceCidrs(getDefaultSourceCidrs(opt.value, topology));
                      }
                    }}
                  >
                    <Icon className="h-3.5 w-3.5" />
                    {opt.label}
                  </button>
                );
              })}
            </div>
            <p className="text-[11px] text-muted-foreground">
              {t(typeOptions.find((opt) => opt.value === type)?.descKey ?? 'tunnels.protocolDescTcp')}
            </p>
            <FieldErrorText error={fieldError} fields={['target.type', 'ingress.type']} />
          </div>

          {!isSocks5 && (
            <div className="space-y-2">
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className="block text-sm font-medium">{isClientToClient ? t('tunnels.targetAddress') : t('tunnels.localIp')}</label>
                  <Input
                    aria-label={isClientToClient ? t('tunnels.targetAddress') : t('tunnels.localIp')}
                    placeholder="127.0.0.1"
                    value={localIp}
                    onChange={(e) => {
                      clearMutationFeedback();
                      setLocalIp(e.target.value);
                    }}
                  />
                  <FieldErrorText error={fieldError} fields={['target.config.ip', 'target.config.host', 'target.config', 'local_ip']} />
                </div>
                <div className="space-y-1.5">
                  <label className="block text-sm font-medium">{isClientToClient ? t('tunnels.targetPort') : t('tunnels.localPort')}</label>
                  <Input
                    aria-label={isClientToClient ? t('tunnels.targetPort') : t('tunnels.localPort')}
                    type="text"
                    inputMode="numeric"
                    pattern="[0-9]*"
                    placeholder="e.g. 22"
                    value={localPort}
                    onChange={(e) => {
                      clearMutationFeedback();
                      setLocalPort(e.target.value);
                    }}
                  />
                  <FieldErrorText error={fieldError} fields={['target.config.port', 'local_port']} />
                  {localPort && !parsedLocalPort && (
                    <p className="text-[11px] font-medium text-destructive">{portErrorMessage}</p>
                  )}
                </div>
                {!isHttp && isClientToClient && (
                  <div className="space-y-1.5">
                    <label className="block text-sm font-medium">{t('tunnels.bindAddress')}</label>
                    <Input
                      aria-label={t('tunnels.bindAddress')}
                      placeholder="0.0.0.0"
                      value={bindIp}
                      onChange={(e) => {
                        clearMutationFeedback();
                        setBindIp(e.target.value);
                      }}
                      autoCapitalize="none"
                      autoCorrect="off"
                      spellCheck={false}
                    />
                    <FieldErrorText error={fieldError} fields={['ingress.config.bind_ip', 'bind_ip']} />
                  </div>
                )}
                {!isHttp && (
                  <div className={cn('space-y-1.5', !isClientToClient && 'max-w-[220px]')}>
                    <label className="block text-sm font-medium">{isClientToClient ? t('tunnels.bindPort') : t('tunnels.publicPort')}</label>
                    <Input
                      aria-label={isClientToClient ? t('tunnels.bindPort') : t('tunnels.publicPort')}
                      type="text"
                      inputMode="numeric"
                      pattern="[0-9]*"
                      placeholder="18080"
                      value={remotePort}
                      onChange={(e) => {
                        clearMutationFeedback();
                        setRemotePort(e.target.value);
                      }}
                      onBlur={validateRemotePortRange}
                    />
                    <FieldErrorText error={fieldError} fields={['remote_port', 'ingress.config.port']} />
                    {remotePort && !parsedRemotePort && (
                      <p className="text-[11px] font-medium text-destructive">{portErrorMessage}</p>
                    )}
                    {suggestedRemotePort && (
                      <button
                        type="button"
                        className="text-[11px] font-medium text-primary hover:underline"
                        onClick={() => {
                          clearMutationFeedback();
                          setRemotePort(suggestedRemotePort);
                        }}
                      >
                        {t('tunnels.suggestionPrefix')}：{suggestedRemotePort}
                      </button>
                    )}
                    {!isClientToClient && (
                      <div className="mt-1.5 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                        <span>{t('tunnels.portRangeAllowed')}</span>
                        <Badge variant="secondary" className="font-mono">
                          {status?.allowed_ports === undefined
                            ? t('common.loading')
                            : formatPortRanges(status.allowed_ports)}
                        </Badge>
                      </div>
                    )}
                  </div>
                )}
              </div>
              <p className="text-[11px] text-muted-foreground">
                {isClientToClient ? t('tunnels.targetHintClientToClient') : t('tunnels.targetHintServerExpose')}
              </p>
            </div>
          )}

          {isHttp ? (
            <div className="space-y-1.5">
              <label className="text-sm font-medium">{t('tunnels.domain')}</label>
              <Input
                aria-label={t('tunnels.domain')}
                placeholder="e.g. app.example.com"
                value={domain}
                onChange={(e) => {
                  clearMutationFeedback();
                  setDomain(e.target.value);
                }}
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
              />
              <FieldErrorText error={fieldError} fields={['domain', 'ingress.config.domain']} />
              <p className="text-[11px] text-muted-foreground mt-1.5">
                {t('tunnels.httpDomainHelp')}
              </p>
            </div>
          ) : isSocks5 ? (
            <div className={cn('grid gap-3', isClientToClient ? 'grid-cols-2' : 'grid-cols-1')}>
              {isClientToClient && (
                <div className="space-y-1.5">
                  <label className="block text-sm font-medium">{t('tunnels.bindAddress')}</label>
                  <Input
                    aria-label={t('tunnels.bindAddress')}
                    placeholder="0.0.0.0"
                    value={bindIp}
                    onChange={(e) => {
                      clearMutationFeedback();
                      setBindIp(e.target.value);
                    }}
                    autoCapitalize="none"
                    autoCorrect="off"
                    spellCheck={false}
                  />
                  <FieldErrorText error={fieldError} fields={['ingress.config.bind_ip', 'bind_ip']} />
                </div>
              )}
              <div className="space-y-1.5">
                <label className="block text-sm font-medium">{isClientToClient ? t('tunnels.bindPort') : t('tunnels.publicPort')}</label>
                <Input
                  className={isClientToClient ? undefined : 'max-w-[220px]'}
                  aria-label={isClientToClient ? t('tunnels.bindPort') : t('tunnels.publicPort')}
                  type="text"
                  inputMode="numeric"
                  pattern="[0-9]*"
                  placeholder="18080"
                  value={remotePort}
                  onChange={(e) => {
                    clearMutationFeedback();
                    setRemotePort(e.target.value);
                  }}
                  onBlur={validateRemotePortRange}
                />
                <FieldErrorText error={fieldError} fields={['remote_port', 'ingress.config.port']} />
                {remotePort && !parsedRemotePort && (
                  <p className="text-[11px] font-medium text-destructive">{portErrorMessage}</p>
                )}
                {suggestedRemotePort && (
                  <button
                    type="button"
                    className="text-[11px] font-medium text-primary hover:underline"
                    onClick={() => {
                      clearMutationFeedback();
                      setRemotePort(suggestedRemotePort);
                    }}
                  >
                    {t('tunnels.suggestionPrefix')}：{suggestedRemotePort}
                  </button>
                )}
                {!isClientToClient && (
                  <div className="mt-1.5 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                    <span>{t('tunnels.portRangeAllowed')}</span>
                    <Badge variant="secondary" className="font-mono">
                      {status?.allowed_ports === undefined
                        ? t('common.loading')
                        : formatPortRanges(status.allowed_ports)}
                    </Badge>
                  </div>
                )}
              </div>
            </div>
          ) : null}
        </div>

        <div className="rounded-lg border border-border bg-muted/20">
          <button
            type="button"
            className="flex w-full items-center justify-between px-3 py-2 text-left text-sm font-medium"
            onClick={() => setAdvancedOpen((value) => !value)}
            aria-expanded={advancedOpen}
          >
            <span>{t('tunnels.advancedSettings')}</span>
            <ChevronDown className={cn('h-4 w-4 transition-transform', advancedOpen && 'rotate-180')} />
          </button>
          {advancedOpen && (
            <div className="space-y-4 border-t border-border px-3 py-3">
              <p className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t('tunnels.advancedSecurity')}
              </p>
              <div className="space-y-1.5">
                <label className="text-sm font-medium">{t('tunnels.sourceCidrs')}</label>
                <Input
                  aria-label={t('tunnels.sourceCidrs')}
                  placeholder="0.0.0.0/0, ::/0"
                  value={sourceCidrs}
                  onChange={(e) => {
                    clearMutationFeedback();
                    setSourceCidrs(preserveLoopbackSourceCIDRsOnFirstRestriction(sourceCidrs, e.target.value));
                  }}
                />
                <FieldErrorText error={fieldError} fields={['ingress.config.allowed_source_cidrs', 'ingress.config']} />
                <p className="text-[11px] text-muted-foreground">
                  {t('tunnels.sourceCidrsHelp')}
                </p>
                {showLoopbackCIDRWarning && (
                  <p className="text-[11px] text-amber-600 dark:text-amber-400">
                    {t('tunnels.sourceCidrsLoopbackWarning')}
                  </p>
                )}
                {isHttp && (
                  <p className="text-[11px] text-muted-foreground">
                    {t('tunnels.httpSourceCidrsProxyHelp')}
                  </p>
                )}
              </div>

              {isHttp && (
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="tunnel-http-auth"
                      checked={httpAuthEnabled}
                      onCheckedChange={(checked) => {
                        clearMutationFeedback();
                        setHttpAuthEnabled(checked === true);
                      }}
                    />
                    <label htmlFor="tunnel-http-auth" className="text-sm font-medium">
                      {t('tunnels.httpAuth')}
                    </label>
                  </div>
                  {httpAuthEnabled && (
                    <div className="grid grid-cols-2 gap-3">
                      <Input
                        aria-label={t('tunnels.httpUsername')}
                        placeholder={t('tunnels.httpUsername')}
                        value={httpUsername}
                        onChange={(e) => {
                          clearMutationFeedback();
                          setHttpUsername(e.target.value);
                        }}
                      />
                      <Input
                        aria-label={t('tunnels.httpPassword')}
                        placeholder={t('tunnels.httpPassword')}
                        type="password"
                        value={httpPassword}
                        onChange={(e) => {
                          clearMutationFeedback();
                          setHttpPassword(e.target.value);
                        }}
                      />
                    </div>
                  )}
                  <FieldErrorText error={fieldError} fields={['ingress.config.auth', 'ingress.config.auth.username', 'ingress.config.auth.password']} />
                </div>
              )}

              {isSocks5 && (
                <div className="space-y-3">
                  <p className="text-[11px] text-muted-foreground">
                    {t('tunnels.socks5TargetHelp')}
                  </p>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-1.5">
                      <label className="text-sm font-medium">{t('tunnels.socks5TargetCidrs')}</label>
                      <Input
                        aria-label={t('tunnels.socks5TargetCidrs')}
                        placeholder="0.0.0.0/0, ::/0"
                        value={socks5TargetCidrs}
                        onChange={(e) => {
                          clearMutationFeedback();
                          setSocks5TargetCidrs(e.target.value);
                        }}
                      />
                      <FieldErrorText error={fieldError} fields={['target.config.allowed_target_cidrs', 'target.config']} />
                    </div>
                    <div className="space-y-1.5">
                      <label className="text-sm font-medium">{t('tunnels.socks5TargetPorts')}</label>
                      <Input
                        aria-label={t('tunnels.socks5TargetPorts')}
                        placeholder={t('tunnels.socks5TargetPortsPlaceholder')}
                        value={socks5TargetPorts}
                        onChange={(e) => {
                          clearMutationFeedback();
                          setSocks5TargetPorts(e.target.value);
                        }}
                      />
                      <FieldErrorText error={fieldError} fields={['target.config.allowed_target_ports']} />
                    </div>
                  </div>
                  <div className="space-y-1.5">
                    <label className="text-sm font-medium">{t('tunnels.socks5TargetHosts')}</label>
                    <Input
                      aria-label={t('tunnels.socks5TargetHosts')}
                      placeholder="example.com, 10.0.0.5"
                      value={socks5TargetHosts}
                      onChange={(e) => {
                        clearMutationFeedback();
                        setSocks5TargetHosts(e.target.value);
                      }}
                    />
                    <p className="text-[11px] text-muted-foreground">
                      {t('tunnels.socks5TargetHostsHelp')}
                    </p>
                    <FieldErrorText error={fieldError} fields={['target.config.allowed_target_hosts']} />
                  </div>
                  <div className="space-y-2">
                    <div className="flex items-center gap-2">
                      <Checkbox
                        id="tunnel-socks5-auth"
                        checked={socks5AuthEnabled}
                        onCheckedChange={(checked) => {
                          clearMutationFeedback();
                          setSocks5AuthEnabled(checked === true);
                        }}
                      />
                      <label htmlFor="tunnel-socks5-auth" className="text-sm font-medium">
                        {t('tunnels.socks5Auth')}
                      </label>
                    </div>
                    {socks5AuthEnabled && (
                      <div className="grid grid-cols-2 gap-3">
                        <Input
                          aria-label={t('tunnels.socks5Username')}
                          placeholder={t('tunnels.socks5Username')}
                          value={socks5Username}
                          onChange={(e) => {
                            clearMutationFeedback();
                            setSocks5Username(e.target.value);
                          }}
                        />
                        <Input
                          aria-label={t('tunnels.socks5Password')}
                          placeholder={t('tunnels.socks5Password')}
                          type="password"
                          value={socks5Password}
                          onChange={(e) => {
                            clearMutationFeedback();
                            setSocks5Password(e.target.value);
                          }}
                        />
                      </div>
                    )}
                    <FieldErrorText error={fieldError} fields={['ingress.config.auth', 'ingress.config.auth.username', 'ingress.config.auth.password']} />
                    {!isClientToClient && !socks5AuthEnabled && (
                      <div className="space-y-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2.5">
                        <div className="flex items-start gap-2">
                          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
                          <div className="space-y-0.5">
                            <p className="text-xs font-semibold text-amber-700 dark:text-amber-300">
                              {t('tunnels.socks5NoAuthTitle')}
                            </p>
                            <p className="text-[11px] leading-snug text-amber-700/90 dark:text-amber-200/80">
                              {t('tunnels.socks5NoAuthDesc')}
                            </p>
                          </div>
                        </div>
                        <label
                          htmlFor="tunnel-socks5-no-auth"
                          className="flex items-start gap-2 text-xs font-medium text-amber-800 dark:text-amber-200"
                        >
                          <Checkbox
                            id="tunnel-socks5-no-auth"
                            className="mt-0.5 border-amber-500/60 data-checked:border-amber-600 data-checked:bg-amber-600"
                            checked={confirmNoAuthRisk}
                            onCheckedChange={(checked) => {
                              clearMutationFeedback();
                              setConfirmNoAuthRisk(checked === true);
                            }}
                          />
                          <span>{t('tunnels.socks5NoAuthConfirm')}</span>
                        </label>
                      </div>
                    )}
                    <FieldErrorText error={fieldError} fields={['confirm_no_auth_risk']} />
                  </div>
                </div>
              )}

              <div className="border-t border-border pt-3">
                <p className="mb-3 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                  {t('tunnels.advancedPerformance')}
                </p>
                <div className="grid grid-cols-2 gap-3">
                  {isClientToClient && (
                    <div className="col-span-2 space-y-1.5">
                      <label className="text-sm font-medium">{t('tunnels.totalLimit')}</label>
                      <InputGroup>
                        <InputGroupInput aria-label={t('tunnels.totalLimit')} type="number" step="any" placeholder="0" value={totalBps} onChange={(e) => { clearMutationFeedback(); setTotalBps(e.target.value); }} min={0} />
                        <InputGroupAddon align="inline-end"><InputGroupText>MiB/s</InputGroupText></InputGroupAddon>
                      </InputGroup>
                      <p className="text-xs text-muted-foreground">{t('tunnels.totalLimitHint')}</p>
                      <FieldErrorText error={fieldError} fields={['total_bps', 'bandwidth_settings.total_bps']} />
                    </div>
                  )}
                  <div className="space-y-1.5">
                    <label className="text-sm font-medium">{t('tunnels.ingressLimit')}</label>
                    <InputGroup>
                      <InputGroupInput
                        aria-label={t('tunnels.ingressLimit')}
                        type="number"
                        step="any"
                        placeholder="0"
                        value={ingressBps}
                        onChange={(e) => {
                          clearMutationFeedback();
                          setIngressBps(e.target.value);
                        }}
                        min={0}
                      />
                      <InputGroupAddon align="inline-end">
                        <InputGroupText>MiB/s</InputGroupText>
                      </InputGroupAddon>
                    </InputGroup>
                    <FieldErrorText error={fieldError} fields={['ingress_bps', 'bandwidth_settings.ingress_bps']} />
                  </div>
                  <div className="space-y-1.5">
                    <label className="text-sm font-medium">{t('tunnels.egressLimit')}</label>
                    <InputGroup>
                      <InputGroupInput
                        aria-label={t('tunnels.egressLimit')}
                        type="number"
                        step="any"
                        placeholder="0"
                        value={egressBps}
                        onChange={(e) => {
                          clearMutationFeedback();
                          setEgressBps(e.target.value);
                        }}
                        min={0}
                      />
                      <InputGroupAddon align="inline-end">
                        <InputGroupText>MiB/s</InputGroupText>
                      </InputGroupAddon>
                    </InputGroup>
                    <FieldErrorText error={fieldError} fields={['egress_bps', 'bandwidth_settings.egress_bps']} />
                  </div>
                </div>
              </div>
            </div>
          )}
        </div>

        {mutation.isError && (
          <div className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg mt-2">
            <AlertTriangle className="w-4 h-4 shrink-0" />
            {getTunnelMutationErrorMessage(mutation.error)}
          </div>
        )}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setOpen(false)}
          >
            {t('common.cancel')}
          </Button>
          <Button
            type="submit"
            disabled={mutation.isPending}
          >
            {mutation.isPending
              ? (isEdit ? t('tunnels.updating') : t('tunnels.creating'))
              : (isEdit ? t('tunnels.saveChanges') : t('tunnels.createTunnel'))}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  );
}
