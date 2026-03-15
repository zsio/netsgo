import { useState } from 'react';
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter, DialogTrigger,
} from '@/components/ui/dialog';
import { AlertTriangle } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useCreateTunnel } from '@/hooks/use-tunnel-mutations';
import { useServerStatus } from '@/hooks/use-server-status';
import type { ProxyType } from '@/types';

interface AddTunnelDialogProps {
  clientId: string;
}

const typeOptions: { value: ProxyType; label: string }[] = [
  { value: 'tcp', label: 'TCP' },
  { value: 'udp', label: 'UDP' },
  { value: 'http', label: 'HTTP' },
];

export function AddTunnelDialog({ clientId }: AddTunnelDialogProps) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [type, setType] = useState<ProxyType>('tcp');
  const [localIp, setLocalIp] = useState('127.0.0.1');
  const [localPort, setLocalPort] = useState('');
  const [remotePort, setRemotePort] = useState('');

  const createTunnel = useCreateTunnel();
  const { data: status } = useServerStatus({
    enabled: open,
    refetchOnMount: 'always',
    staleTime: 0,
  });

  const resetForm = () => {
    setName('');
    setType('tcp');
    setLocalIp('127.0.0.1');
    setLocalPort('');
    setRemotePort('');
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    createTunnel.mutate(
      {
        clientId,
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
        },
      },
    );
  };

  const isValid = name.trim() && localPort && parseInt(localPort, 10) > 0;

  return (
    <Dialog open={open} onOpenChange={(v) => { setOpen(v); if (!v) resetForm(); }}>
      <DialogTrigger asChild>
        <Button variant="default" className="shadow-sm shadow-primary/20">
          添加隧道
        </Button>
      </DialogTrigger>

      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>创建代理隧道</DialogTitle>
          <DialogDescription>
            配置内网穿透隧道，将 Client 侧的本地服务暴露到公网端口。
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
              autoFocus
            />
          </div>

          {/* 协议类型 */}
          <div className="space-y-1.5">
            <label className="text-sm font-medium">协议类型</label>
            <div className="flex gap-2">
              {typeOptions.map((opt) => (
                <button
                  key={opt.value}
                  type="button"
                  className={`flex-1 py-2 rounded-md text-sm font-medium border transition-colors ${
                    type === opt.value
                      ? 'bg-primary text-primary-foreground border-primary'
                      : 'bg-background text-muted-foreground border-border hover:bg-muted/50'
                  }`}
                  onClick={() => setType(opt.value)}
                >
                  {opt.label}
                </button>
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

          {createTunnel.isError && (
            <div className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg mt-2">
              <AlertTriangle className="w-4 h-4 shrink-0" />
              {(createTunnel.error as Error).message}
            </div>
          )}

          <DialogFooter>
            <Button
              type="submit"
              disabled={!isValid || createTunnel.isPending}
            >
              {createTunnel.isPending ? '创建中…' : '创建隧道'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
