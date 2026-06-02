import { useState } from 'react';
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter, DialogTrigger,
} from '@/components/ui/dialog';
import { AlertTriangle, GitBranchPlus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from '@/components/ui/input-group';
import toast from 'react-hot-toast';
import { useCreateTunnel, useUpdateTunnel } from '@/hooks/use-tunnel-mutations';
import {
  getTunnelMutationErrorMessage,
  getTunnelMutationFieldError,
} from '@/lib/tunnel-model';
import { bpsToMbpsInput, parseMbpsInputToBps } from '@/lib/format';
import { useServerStatus } from '@/hooks/use-server-status';
import { getClientDisplayName } from '@/lib/client-utils';
import { cn } from '@/lib/utils';
import type { Client, PortRange, ProxyType, ProxyConfig, TunnelTopology } from '@/types';
import { i18n } from '@/i18n';
import { useTranslation } from 'react-i18next';

/** 编辑模式下传入的隧道数据 */
export interface TunnelDialogEditData extends ProxyConfig {
  clientId: string;
}

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

const typeOptions: { value: ProxyType; label: string }[] = [
  { value: 'tcp', label: 'TCP' },
  { value: 'udp', label: 'UDP' },
  { value: 'http', label: 'HTTP' },
];

interface TunnelFormState {
  name: string;
  topology: TunnelTopology;
  targetClientId: string;
  ingressClientId: string;
  bindIp: string;
  type: ProxyType;
  localIp: string;
  localPort: string;
  remotePort: string;
  domain: string;
  ingressBps: string;
  egressBps: string;
}

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

function getInitialFormState(props: TunnelDialogProps): TunnelFormState {
  if (props.mode === 'edit' && props.tunnel) {
    return {
      name: props.tunnel.name,
      topology: props.tunnel.topology ?? 'server_expose',
      targetClientId: props.tunnel.target?.client_id ?? props.tunnel.owner_client_id ?? props.tunnel.client_id ?? props.tunnel.clientId,
      ingressClientId: props.tunnel.ingress?.client_id ?? '',
      bindIp: props.tunnel.ingress?.type === 'tcp_listen' || props.tunnel.ingress?.type === 'udp_listen'
        ? props.tunnel.ingress.config.bind_ip
        : '127.0.0.1',
      type: props.tunnel.type,
      localIp: getInitialTargetHost(props.tunnel),
      localPort: String(getInitialTargetPort(props.tunnel) || ''),
      remotePort: String(getInitialIngressPort(props.tunnel) || ''),
      domain: props.tunnel.domain || '',
      ingressBps: bpsToMbpsInput(props.tunnel.ingress_bps),
      egressBps: bpsToMbpsInput(props.tunnel.egress_bps),
    };
  }

  return {
    name: '',
    topology: 'server_expose',
    targetClientId: props.mode === 'create' ? props.clientId : '',
    ingressClientId: '',
    bindIp: '127.0.0.1',
    type: 'tcp',
    localIp: '127.0.0.1',
    localPort: '',
    remotePort: '',
    domain: '',
    ingressBps: '',
    egressBps: '',
  };
}

function getInitialIngressPort(tunnel: TunnelDialogEditData) {
  if (tunnel.ingress?.type === 'tcp_listen' || tunnel.ingress?.type === 'udp_listen') {
    return tunnel.ingress.config.port;
  }
  return tunnel.remote_port;
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

function localFieldError(field: string, message: string): LocalFieldError {
  return { field, message, code: 'invalid_field', source: 'local' };
}

function serverFieldError(error: unknown): LocalFieldError | null {
  return getTunnelMutationFieldError(error);
}

function formatPortRanges(ranges: PortRange[] | undefined) {
  if (!ranges || ranges.length === 0) {
    return i18n.t('tunnels.unrestricted');
  }
  return ranges.map((range) => range.start === range.end ? range.start : `${range.start}-${range.end}`).join(', ');
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
  const initialForm = getInitialFormState(props);
  const [name, setName] = useState(initialForm.name);
  const [topology, setTopology] = useState<TunnelTopology>(initialForm.topology);
  const [targetClientId, setTargetClientId] = useState(initialForm.targetClientId);
  const [ingressClientId, setIngressClientId] = useState(initialForm.ingressClientId);
  const [bindIp, setBindIp] = useState(initialForm.bindIp);
  const [type, setType] = useState<ProxyType>(initialForm.type);
  const [localIp, setLocalIp] = useState(initialForm.localIp);
  const [localPort, setLocalPort] = useState(initialForm.localPort);
  const [remotePort, setRemotePort] = useState(initialForm.remotePort);
  const [domain, setDomain] = useState(initialForm.domain);
  const [ingressBps, setIngressBps] = useState(initialForm.ingressBps);
  const [egressBps, setEgressBps] = useState(initialForm.egressBps);
  const [fieldError, setFieldError] = useState<LocalFieldError | null>(null);

  const clients = props.clients ?? [];
  const selectedTargetClientId = targetClientId || (props.mode === 'create' ? props.clientId : props.tunnel?.target?.client_id ?? props.tunnel?.owner_client_id ?? props.tunnel?.clientId ?? '');
  const sourceClient = clients.find((client) => client.id === selectedTargetClientId);
  const ingressClientOptions = clients.filter((client) => client.id !== selectedTargetClientId);
  const selectedIngressClientId = ingressClientId && ingressClientId !== selectedTargetClientId
    ? ingressClientId
    : ingressClientOptions[0]?.id || '';
  const isClientToClient = topology === 'client_to_client';
  const isHttp = type === 'http';
  const canUseClientToClient = ingressClientOptions.length > 0;
  const parsedLocalPort = parsePortInput(localPort);
  const parsedRemotePort = isHttp ? 0 : parsePortInput(remotePort);
  const selectClassName = cn(
    'h-8 w-full rounded-lg border border-input bg-background px-2.5 py-1 text-sm outline-none transition-colors',
    'focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50',
    'disabled:pointer-events-none disabled:cursor-not-allowed disabled:bg-input/50 disabled:opacity-50',
  );

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

    if (!parsedLocalPort) {
      setFieldError(localFieldError('local_port', portErrorMessage));
      return;
    }

    if (!isHttp && !parsedRemotePort) {
      setFieldError(localFieldError('remote_port', portErrorMessage));
      return;
    }

    if (isClientToClient && !canUseClientToClient) {
      setFieldError(localFieldError('ingress.client_id', t('tunnels.c2cRequiresTwoClients')));
      return;
    }

    if (parsedIngressBps == null || parsedEgressBps == null) {
      toast.error(t('tunnels.bandwidthNonNegative'));
      return;
    }

    if (!isClientToClient && !isHttp && parsedRemotePort && !isPortAllowedByRanges(parsedRemotePort, status?.allowed_ports)) {
      const message = t('tunnels.portMustBeAllowed', { ranges: formatPortRanges(status?.allowed_ports) });
      setFieldError({ field: 'remote_port', message, code: 'port_not_allowed' });
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
          local_ip: localIp,
          local_port: parsedLocalPort,
          remote_port: parsedRemotePort ?? 0,
          domain,
          ingress_bps: parsedIngressBps,
          egress_bps: parsedEgressBps,
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
        local_ip: localIp,
        local_port: parsedLocalPort,
        remote_port: parsedRemotePort ?? 0,
        domain,
        ingress_bps: parsedIngressBps,
        egress_bps: parsedEgressBps,
      },
      {
        onSuccess: () => {
          setFieldError(null);
          setOpen(false);
          toast.success(t('tunnels.created', { name }));
        },
        onError: (err) => {
          setFieldError(serverFieldError(err));
          toast.error(getTunnelMutationErrorMessage(err));
        },
      },
    );
  };

  const parsedIngressBps = parseMbpsInputToBps(ingressBps);
  const parsedEgressBps = parseMbpsInputToBps(egressBps);
  const effectiveTypeOptions = isClientToClient
    ? typeOptions.filter((opt) => opt.value !== 'http')
    : typeOptions;
  const isValid = Boolean(
    name.trim()
    && selectedTargetClientId
    && parsedLocalPort !== null
    && (isClientToClient ? canUseClientToClient && selectedIngressClientId && bindIp.trim() && type !== 'http' : true)
    && (isHttp ? domain.trim() : parsedRemotePort !== null)
    && (isClientToClient || isHttp || (parsedRemotePort !== null && isPortAllowedByRanges(parsedRemotePort, status?.allowed_ports)))
    && parsedIngressBps !== null
    && parsedEgressBps !== null,
  );

  return (
    <DialogContent className="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{isEdit ? t('tunnels.editTitle') : t('tunnels.createTitle')}</DialogTitle>
        {props.mode === 'edit' && (
          <DialogDescription>
            {t('tunnels.editDescription', { name: props.tunnel?.name ?? '' })}
          </DialogDescription>
        )}
      </DialogHeader>

      <form onSubmit={handleSubmit} className="space-y-4">
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
          <FieldErrorText error={fieldError} fields={['name']} />
        </div>

        {/* 协议类型 */}
        <div className="space-y-1.5">
          <label className="text-sm font-medium">{t('tunnels.topology')}</label>
          <div className="grid grid-cols-2 gap-2">
            <Button
              type="button"
              variant={topology === 'server_expose' ? 'default' : 'outline'}
              onClick={() => {
                clearMutationFeedback();
                setTopology('server_expose');
              }}
            >
              {t('tunnels.serverExpose')}
            </Button>
            <Button
              type="button"
              variant={topology === 'client_to_client' ? 'default' : 'outline'}
              disabled={!canUseClientToClient}
              onClick={() => {
                if (!canUseClientToClient) {
                  return;
                }
                clearMutationFeedback();
                setTopology('client_to_client');
                if (type === 'http') setType('tcp');
              }}
            >
              {t('tunnels.clientToClient')}
            </Button>
          </div>
          {!canUseClientToClient && (
            <p className="text-[11px] font-medium text-destructive">
              {t('tunnels.c2cRequiresTwoClients')}
            </p>
          )}
          <FieldErrorText error={fieldError} fields={['topology', 'transport_policy']} />
        </div>

        {(isClientToClient || clients.length > 1) && (
          <div className={cn('grid gap-3', isClientToClient ? 'grid-cols-2' : 'grid-cols-1')}>
            <div className="space-y-1.5">
              <label className="text-sm font-medium">{t('tunnels.sourceClient')}</label>
              {clients.length > 0 ? (
                <select
                  aria-label={t('tunnels.sourceClient')}
                  className={selectClassName}
                  value={selectedTargetClientId}
                  disabled={isEdit}
                  onChange={(e) => {
                    clearMutationFeedback();
                    const nextTargetClientId = e.target.value;
                    setTargetClientId(nextTargetClientId);
                    if (ingressClientId === nextTargetClientId) {
                      setIngressClientId('');
                    }
                  }}
                >
                  {clients.map((client) => (
                    <option key={client.id} value={client.id}>
                      {getClientDisplayName(client)}
                    </option>
                  ))}
                </select>
              ) : (
                <Input value={sourceClient ? getClientDisplayName(sourceClient) : selectedTargetClientId} disabled />
              )}
              <FieldErrorText error={fieldError} fields={['target.client_id', 'client_id']} />
            </div>
            {isClientToClient && (
              <div className="space-y-1.5">
                <label className="text-sm font-medium">{t('tunnels.ingressClient')}</label>
                <select
                  aria-label={t('tunnels.ingressClient')}
                  className={selectClassName}
                  value={selectedIngressClientId}
                  onChange={(e) => {
                    clearMutationFeedback();
                    setIngressClientId(e.target.value);
                  }}
                >
                  {ingressClientOptions.map((client) => (
                    <option key={client.id} value={client.id}>
                      {getClientDisplayName(client)}
                    </option>
                  ))}
                </select>
                <FieldErrorText error={fieldError} fields={['ingress.client_id']} />
              </div>
            )}
          </div>
        )}

        <div className="space-y-1.5">
          <label className="text-sm font-medium">{t('tunnels.protocolType')}</label>
          <div className="flex gap-2">
            {effectiveTypeOptions.map((opt) => (
              <Button
                key={opt.value}
                type="button"
                variant={type === opt.value ? 'default' : 'outline'}
                className="flex-1"
                onClick={() => {
                  clearMutationFeedback();
                  setType(opt.value);
                }}
              >
                {opt.label}
              </Button>
            ))}
          </div>
          <FieldErrorText error={fieldError} fields={['target.type', 'ingress.type']} />
        </div>

        {/* 本地地址 */}
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <label className="text-sm font-medium">{isClientToClient ? t('tunnels.targetAddress') : t('tunnels.localIp')}</label>
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
            <label className="text-sm font-medium">{isClientToClient ? t('tunnels.targetPort') : t('tunnels.localPort')}</label>
            <Input
              aria-label={isClientToClient ? t('tunnels.targetPort') : t('tunnels.localPort')}
              type="number"
              placeholder="e.g. 22"
              value={localPort}
              onChange={(e) => {
                clearMutationFeedback();
                setLocalPort(e.target.value);
              }}
              min={1}
              max={65535}
            />
            <FieldErrorText error={fieldError} fields={['target.config.port', 'local_port']} />
            {localPort && !parsedLocalPort && (
              <p className="text-[11px] font-medium text-destructive">{portErrorMessage}</p>
            )}
          </div>
        </div>

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
        ) : (
          <div className={cn('grid gap-3', isClientToClient ? 'grid-cols-2' : 'grid-cols-1')}>
            {isClientToClient && (
              <div className="space-y-1.5">
                <label className="text-sm font-medium">{t('tunnels.bindAddress')}</label>
                <Input
                  aria-label={t('tunnels.bindAddress')}
                  placeholder="127.0.0.1 / 0.0.0.0"
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
              <label className="text-sm font-medium">{isClientToClient ? t('tunnels.bindPort') : t('tunnels.publicPort')}</label>
              <Input
                aria-label={isClientToClient ? t('tunnels.bindPort') : t('tunnels.publicPort')}
                type="number"
                placeholder="e.g. 18080"
                value={remotePort}
                onChange={(e) => {
                  clearMutationFeedback();
                  setRemotePort(e.target.value);
                }}
                min={1}
                max={65535}
              />
              <FieldErrorText error={fieldError} fields={['remote_port', 'ingress.config.port']} />
              {remotePort && !parsedRemotePort && (
                <p className="text-[11px] font-medium text-destructive">{portErrorMessage}</p>
              )}
              {!isClientToClient && (
                <p className="text-[11px] text-muted-foreground mt-1.5">
                  {t('tunnels.portRangeAllowed')}
                  {status?.allowed_ports === undefined
                    ? t('common.loading')
                    : formatPortRanges(status.allowed_ports)}
                </p>
              )}
            </div>
          </div>
        )}

        <div className="grid grid-cols-2 gap-3">
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
        <p className="text-[11px] text-muted-foreground -mt-1">
          {t('tunnels.bandwidthHelp')}
        </p>

        {mutation.isError && (
          <div className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg mt-2">
            <AlertTriangle className="w-4 h-4 shrink-0" />
            {getTunnelMutationErrorMessage(mutation.error)}
          </div>
        )}

        <DialogFooter>
          {isEdit && (
            <Button
              type="button"
              variant="outline"
              onClick={() => setOpen(false)}
            >
              {t('common.cancel')}
            </Button>
          )}
          <Button
            type="submit"
            disabled={!isValid || mutation.isPending}
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
