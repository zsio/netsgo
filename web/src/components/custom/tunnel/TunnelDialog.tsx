import { useState } from 'react';
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter, DialogTrigger,
} from '@/components/ui/dialog';
import { AlertTriangle } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import toast from 'react-hot-toast';
import { useCreateTunnel, useUpdateTunnel } from '@/hooks/use-tunnel-mutations';
import { getTunnelMutationErrorMessage } from '@/lib/tunnel-model';
import { useServerStatus } from '@/hooks/use-server-status';
import type { ProxyType, ProxyConfig } from '@/types';

/** 编辑模式下传入的隧道数据 */
export interface TunnelDialogEditData extends ProxyConfig {
  clientId: string;
}

interface TunnelDialogCreateProps {
  mode: 'create';
  clientId: string;
  /** 触发按钮（作为 DialogTrigger children） */
  trigger?: React.ReactNode;
}

interface TunnelDialogEditProps {
  mode: 'edit';
  tunnel: TunnelDialogEditData | null;
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
  type: ProxyType;
  localIp: string;
  localPort: string;
  remotePort: string;
  domain: string;
}

function getInitialFormState(props: TunnelDialogProps): TunnelFormState {
  if (props.mode === 'edit' && props.tunnel) {
    return {
      name: props.tunnel.name,
      type: props.tunnel.type,
      localIp: props.tunnel.local_ip || '127.0.0.1',
      localPort: String(props.tunnel.local_port || ''),
      remotePort: String(props.tunnel.remote_port || ''),
      domain: props.tunnel.domain || '',
    };
  }

  return {
    name: '',
    type: 'tcp',
    localIp: '127.0.0.1',
    localPort: '',
    remotePort: '',
    domain: '',
  };
}

function getFormKey(props: TunnelDialogProps, open: boolean) {
  if (props.mode === 'edit') {
    const tunnelKey = props.tunnel
      ? `${props.tunnel.clientId}:${props.tunnel.name}`
      : 'empty';
    return `edit:${tunnelKey}:${open ? 'open' : 'closed'}`;
  }

  return `create:${props.clientId}:${open ? 'open' : 'closed'}`;
}

export function TunnelDialog(props: TunnelDialogProps) {
  const isEdit = props.mode === 'edit';

  // --- 弹窗开关 ---
  const [internalOpen, setInternalOpen] = useState(false);
  const open = isEdit ? props.open : internalOpen;
  const setOpen = isEdit
    ? props.onOpenChange
    : setInternalOpen;

  const formKey = getFormKey(props, open);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {!isEdit && (
        <DialogTrigger asChild>
          {(props as TunnelDialogCreateProps).trigger ?? (
            <Button>添加隧道</Button>
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
  const isEdit = props.mode === 'edit';
  const initialForm = getInitialFormState(props);
  const [name, setName] = useState(initialForm.name);
  const [type, setType] = useState<ProxyType>(initialForm.type);
  const [localIp, setLocalIp] = useState(initialForm.localIp);
  const [localPort, setLocalPort] = useState(initialForm.localPort);
  const [remotePort, setRemotePort] = useState(initialForm.remotePort);
  const [domain, setDomain] = useState(initialForm.domain);

  const isHttp = type === 'http';

  const createTunnel = useCreateTunnel();
  const updateTunnel = useUpdateTunnel();
  const mutation = isEdit ? updateTunnel : createTunnel;

  const { data: status } = useServerStatus({
    enabled: open,
    refetchOnMount: 'always',
    staleTime: 0,
  });

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const parsedLocalPort = Number.parseInt(localPort, 10);
    const parsedRemotePort = remotePort ? Number.parseInt(remotePort, 10) : 0;

    if (props.mode === 'edit') {
      const tunnel = props.tunnel;
      if (!tunnel) return;

      updateTunnel.mutate(
        {
          clientId: tunnel.clientId,
          tunnelName: tunnel.name,
          type: tunnel.type,
          local_ip: localIp,
          local_port: parsedLocalPort,
          remote_port: parsedRemotePort,
          domain,
        },
        {
          onSuccess: () => {
            setOpen(false);
            toast.success(`隧道「${tunnel.name}」已更新`);
          },
          onError: (err) => {
            toast.error(getTunnelMutationErrorMessage(err));
          },
        },
      );
      return;
    }

    createTunnel.mutate(
      {
        clientId: props.clientId,
        name,
        type,
        local_ip: localIp,
        local_port: parsedLocalPort,
        remote_port: parsedRemotePort,
        domain,
      },
      {
        onSuccess: () => {
          setOpen(false);
          toast.success(`隧道「${name}」创建成功`);
        },
        onError: (err) => {
          toast.error(getTunnelMutationErrorMessage(err));
        },
      },
    );
  };

  const parsedRemotePort = Number.parseInt(remotePort, 10);
  const isValid = isEdit
    ? Boolean(localPort && Number.parseInt(localPort, 10) > 0 && (isHttp ? domain.trim() : parsedRemotePort > 0))
    : Boolean(name.trim() && localPort && Number.parseInt(localPort, 10) > 0 && (isHttp ? domain.trim() : parsedRemotePort > 0));

  return (
    <DialogContent className="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{isEdit ? '编辑隧道' : '创建代理隧道'}</DialogTitle>
        <DialogDescription>
          {props.mode === 'edit'
            ? `修改隧道「${props.tunnel?.name}」的映射配置。隧道名称和协议类型不可变更。`
            : '配置内网穿透隧道。TCP / UDP 通过公网端口映射，HTTP 通过域名分流到 Client 侧服务。'}
        </DialogDescription>
      </DialogHeader>

      <form onSubmit={handleSubmit} className="space-y-4">
        {/* 隧道名称 */}
        <div className="space-y-1.5">
          <label className="text-sm font-medium">隧道名称</label>
          <Input
            placeholder="例如 ssh-dev"
            value={name}
            onChange={(e) => setName(e.target.value)}
            disabled={isEdit}
            className={isEdit ? 'opacity-60' : undefined}
            autoFocus={!isEdit}
          />
        </div>

        {/* 协议类型 */}
        <div className="space-y-1.5">
          <label className="text-sm font-medium">协议类型</label>
          <div className="flex gap-2">
            {typeOptions.map((opt) => (
              <Button
                key={opt.value}
                type="button"
                variant={type === opt.value ? 'default' : 'outline'}
                className="flex-1"
                onClick={() => !isEdit && setType(opt.value)}
                disabled={isEdit}
              >
                {opt.label}
              </Button>
            ))}
          </div>
        </div>

        {/* 本地地址 */}
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <label className="text-sm font-medium">本地 IP</label>
            <Input
              placeholder="127.0.0.1"
              value={localIp}
              onChange={(e) => setLocalIp(e.target.value)}
              autoFocus={isEdit}
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium">本地端口</label>
            <Input
              type="number"
              placeholder="22"
              value={localPort}
              onChange={(e) => setLocalPort(e.target.value)}
              min={1}
              max={65535}
            />
          </div>
        </div>

        {isHttp ? (
          <div className="space-y-1.5">
            <label className="text-sm font-medium">业务域名</label>
            <Input
              placeholder="例如 app.example.com"
              value={domain}
              onChange={(e) => setDomain(e.target.value)}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <p className="text-[11px] text-muted-foreground mt-1.5">
              HTTP 隧道按域名分流，不再使用公网端口作为用户输入。
            </p>
          </div>
        ) : (
          <div className="space-y-1.5">
            <label className="text-sm font-medium">公网端口</label>
            <Input
              type="number"
              placeholder="e.g. 18080"
              value={remotePort}
              onChange={(e) => setRemotePort(e.target.value)}
              min={1}
              max={65535}
            />
            {status?.allowed_ports && (
              <p className="text-[11px] text-muted-foreground mt-1.5">
                可用端口范围：
                {status.allowed_ports.length > 0
                  ? status.allowed_ports.map(p => p.start === p.end ? p.start : `${p.start}-${p.end}`).join(', ')
                  : '1-65535 (无限制)'}
              </p>
            )}
          </div>
        )}

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
              取消
            </Button>
          )}
          <Button
            type="submit"
            disabled={!isValid || mutation.isPending}
          >
            {mutation.isPending
              ? (isEdit ? '保存中…' : '创建中…')
              : (isEdit ? '保存修改' : '创建隧道')}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  );
}
