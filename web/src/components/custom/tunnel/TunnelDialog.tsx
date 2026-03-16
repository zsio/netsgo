import { useState, useEffect } from 'react';
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter, DialogTrigger,
} from '@/components/ui/dialog';
import { AlertTriangle } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import toast from 'react-hot-toast';
import { useCreateTunnel, useUpdateTunnel } from '@/hooks/use-tunnel-mutations';
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

export function TunnelDialog(props: TunnelDialogProps) {
  const isEdit = props.mode === 'edit';

  // --- 弹窗开关 ---
  const [internalOpen, setInternalOpen] = useState(false);
  const open = isEdit ? props.open : internalOpen;
  const setOpen = isEdit
    ? props.onOpenChange
    : setInternalOpen;

  // --- 表单状态 ---
  const [name, setName] = useState('');
  const [type, setType] = useState<ProxyType>('tcp');
  const [localIp, setLocalIp] = useState('127.0.0.1');
  const [localPort, setLocalPort] = useState('');
  const [remotePort, setRemotePort] = useState('');

  // --- mutations ---
  const createTunnel = useCreateTunnel();
  const updateTunnel = useUpdateTunnel();
  const mutation = isEdit ? updateTunnel : createTunnel;

  const { data: status } = useServerStatus({
    enabled: open,
    refetchOnMount: 'always',
    staleTime: 0,
  });

  // 编辑模式：打开时用隧道数据填充表单
  useEffect(() => {
    if (isEdit && props.tunnel && open) {
      setName(props.tunnel.name);
      setType(props.tunnel.type);
      setLocalIp(props.tunnel.local_ip || '127.0.0.1');
      setLocalPort(String(props.tunnel.local_port || ''));
      setRemotePort(String(props.tunnel.remote_port || ''));
    }
  }, [isEdit, isEdit && (props as TunnelDialogEditProps).tunnel, open]);

  const resetForm = () => {
    setName('');
    setType('tcp');
    setLocalIp('127.0.0.1');
    setLocalPort('');
    setRemotePort('');
    mutation.reset();
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();

    if (isEdit) {
      const tunnel = props.tunnel;
      if (!tunnel) return;
      updateTunnel.mutate(
        {
          clientId: tunnel.clientId,
          tunnelName: tunnel.name,
          local_ip: localIp,
          local_port: parseInt(localPort, 10),
          remote_port: remotePort ? parseInt(remotePort, 10) : 0,
          domain: tunnel.domain ?? '',
        },
        {
          onSuccess: () => {
            setOpen(false);
            toast.success(`隧道「${tunnel.name}」已更新`);
          },
          onError: (err) => {
            toast.error((err as Error).message);
          },
        },
      );
    } else {
      createTunnel.mutate(
        {
          clientId: props.clientId,
          name,
          type,
          local_ip: localIp,
          local_port: parseInt(localPort, 10),
          remote_port: remotePort ? parseInt(remotePort, 10) : 0,
        },
        {
          onSuccess: () => {
            setOpen(false);
            resetForm();
            toast.success(`隧道「${name}」创建成功`);
          },
          onError: (err) => {
            toast.error((err as Error).message);
          },
        },
      );
    }
  };

  const isValid = isEdit
    ? localPort && parseInt(localPort, 10) > 0
    : name.trim() && localPort && parseInt(localPort, 10) > 0;

  const dialogContent = (
    <DialogContent className="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{isEdit ? '编辑隧道' : '创建代理隧道'}</DialogTitle>
        <DialogDescription>
          {isEdit
            ? `修改隧道「${(props as TunnelDialogEditProps).tunnel?.name}」的映射配置。隧道名称和协议类型不可变更。`
            : '配置内网穿透隧道，将 Client 侧的本地服务暴露到公网端口。'}
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

        {/* 公网端口 */}
        <div className="space-y-1.5">
          <label className="text-sm font-medium">
            公网端口
            <span className="text-muted-foreground font-normal ml-1">(0 = 自动分配)</span>
          </label>
          <Input
            type="number"
            placeholder="0"
            value={remotePort}
            onChange={(e) => setRemotePort(e.target.value)}
            min={0}
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

        {mutation.isError && (
          <div className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg mt-2">
            <AlertTriangle className="w-4 h-4 shrink-0" />
            {(mutation.error as Error).message}
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

  return (
    <Dialog open={open} onOpenChange={(v) => { setOpen(v); if (!v) resetForm(); }}>
      {!isEdit && (
        <DialogTrigger asChild>
          {(props as TunnelDialogCreateProps).trigger ?? (
            <Button>添加隧道</Button>
          )}
        </DialogTrigger>
      )}
      {dialogContent}
    </Dialog>
  );
}
