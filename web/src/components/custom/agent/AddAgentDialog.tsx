import { useState, useCallback } from 'react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Key, Copy, Check, RefreshCcw, Terminal, Link2, Loader2,
} from 'lucide-react';
import { useCreateAPIKey } from '@/hooks/use-admin-keys';
import { useServerStatus } from '@/hooks/use-server-status';

/** 过期时间选项 */
const EXPIRY_OPTIONS = [
  { label: '1 小时', value: '1h' },
  { label: '3 小时', value: '3h' },
  { label: '1 天', value: '24h' },
  { label: '7 天', value: '168h' },
  { label: '不限制', value: '0' },
] as const;

interface AddAgentDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function AddAgentDialog({ open, onOpenChange }: AddAgentDialogProps) {
  const [step, setStep] = useState<'config' | 'result'>('config');
  const [maxUses, setMaxUses] = useState(0);
  const [expiresIn, setExpiresIn] = useState('0');
  const [generatedKey, setGeneratedKey] = useState('');
  const [serverAddr, setServerAddr] = useState('');
  const [copied, setCopied] = useState<'key' | 'cmd' | 'link' | null>(null);

  const createKey = useCreateAPIKey();
  const { data: status } = useServerStatus({
    enabled: open,
    refetchOnMount: 'always',
    staleTime: 0,
  });

  const handleReset = useCallback(() => {
    setStep('config');
    setMaxUses(0);
    setExpiresIn('0');
    setGeneratedKey('');
    setServerAddr('');
    setCopied(null);
  }, []);

  const handleOpenChange = useCallback((v: boolean) => {
    if (!v) handleReset();
    onOpenChange(v);
  }, [onOpenChange, handleReset]);

  const handleGenerate = useCallback(() => {
    const name = `quick-${Date.now().toString(36)}`;
    createKey.mutate(
      {
        name,
        permissions: ['connect'],
        max_uses: maxUses,
        expires_in: expiresIn,
      },
      {
        onSuccess: (data) => {
          setGeneratedKey(data.raw_key);
          setServerAddr(data.server_addr || status?.server_addr || window.location.origin);
          setStep('result');
        },
      },
    );
  }, [createKey, maxUses, expiresIn, status]);

  const copyToClipboard = useCallback(async (text: string, tag: 'key' | 'cmd' | 'link') => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(tag);
      setTimeout(() => setCopied(null), 2000);
    } catch {
      // fallback
    }
  }, []);

  const connectCmd = generatedKey
    ? `netsgo agent --server ${serverAddr} --key ${generatedKey}`
    : '';

  const connectLink = generatedKey
    ? `${serverAddr}/connect?key=${encodeURIComponent(generatedKey)}`
    : '';

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Key className="h-5 w-5 text-primary" />
            添加 Agent
          </DialogTitle>
          <DialogDescription>
            生成临时连接密钥，供新 Agent 快速接入
          </DialogDescription>
        </DialogHeader>

        {step === 'config' && (
          <div className="space-y-5 pt-1">
            {/* 生效次数 */}
            <div className="space-y-2">
              <label className="text-sm font-medium text-foreground">
                生效次数
              </label>
              <div className="flex items-center gap-2">
                <Input
                  type="number"
                  min={0}
                  value={maxUses}
                  onChange={e => setMaxUses(Math.max(0, parseInt(e.target.value) || 0))}
                  className="w-28 text-center"
                  placeholder="0"
                />
                <span className="text-xs text-muted-foreground">
                  {maxUses === 0 ? '无限制' : `最多可使用 ${maxUses} 次`}
                </span>
              </div>
            </div>

            {/* 失效时间 */}
            <div className="space-y-2">
              <label className="text-sm font-medium text-foreground">
                失效时间
              </label>
              <div className="flex flex-wrap gap-2">
                {EXPIRY_OPTIONS.map(opt => (
                  <button
                    key={opt.value}
                    type="button"
                    onClick={() => setExpiresIn(opt.value)}
                    className={`px-3 py-1.5 text-xs rounded-lg border transition-all duration-150 cursor-pointer ${
                      expiresIn === opt.value
                        ? 'bg-primary text-primary-foreground border-primary shadow-sm'
                        : 'bg-muted/50 text-muted-foreground border-border hover:bg-muted hover:text-foreground'
                    }`}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </div>

            {/* 生成按钮 */}
            <Button
              className="w-full"
              onClick={handleGenerate}
              disabled={createKey.isPending}
            >
              {createKey.isPending ? (
                <>
                  <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                  生成中...
                </>
              ) : (
                <>
                  <Key className="h-4 w-4 mr-2" />
                  生成连接密钥
                </>
              )}
            </Button>

            {createKey.isError && (
              <p className="text-xs text-destructive text-center">
                生成失败，请重试
              </p>
            )}
          </div>
        )}

        {step === 'result' && (
          <div className="space-y-4 pt-1">
            {/* 生成的 Key */}
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                连接密钥
              </label>
              <div className="flex items-center gap-2">
                <code className="flex-1 px-3 py-2 text-xs font-mono bg-muted rounded-lg border border-border break-all select-all">
                  {generatedKey}
                </code>
                <Button
                  variant="ghost"
                  size="icon"
                  className="shrink-0"
                  onClick={() => copyToClipboard(generatedKey, 'key')}
                >
                  {copied === 'key' ? (
                    <Check className="h-4 w-4 text-green-500" />
                  ) : (
                    <Copy className="h-4 w-4" />
                  )}
                </Button>
              </div>
              <p className="text-[10px] text-muted-foreground">
                ⚠️ 密钥仅显示一次，请妥善保存
              </p>
            </div>

            {/* 连接命令 */}
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-muted-foreground uppercase tracking-wider flex items-center gap-1.5">
                <Terminal className="h-3.5 w-3.5" />
                连接命令
              </label>
              <div className="flex items-center gap-2">
                <code className="flex-1 px-3 py-2 text-xs font-mono bg-muted rounded-lg border border-border break-all select-all">
                  {connectCmd}
                </code>
                <Button
                  variant="ghost"
                  size="icon"
                  className="shrink-0"
                  onClick={() => copyToClipboard(connectCmd, 'cmd')}
                >
                  {copied === 'cmd' ? (
                    <Check className="h-4 w-4 text-green-500" />
                  ) : (
                    <Copy className="h-4 w-4" />
                  )}
                </Button>
              </div>
            </div>

            {/* 快速连接链接 */}
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-muted-foreground uppercase tracking-wider flex items-center gap-1.5">
                <Link2 className="h-3.5 w-3.5" />
                快速链接
              </label>
              <div className="flex items-center gap-2">
                <code className="flex-1 px-3 py-2 text-xs font-mono bg-muted rounded-lg border border-border break-all select-all">
                  {connectLink}
                </code>
                <Button
                  variant="ghost"
                  size="icon"
                  className="shrink-0"
                  onClick={() => copyToClipboard(connectLink, 'link')}
                >
                  {copied === 'link' ? (
                    <Check className="h-4 w-4 text-green-500" />
                  ) : (
                    <Copy className="h-4 w-4" />
                  )}
                </Button>
              </div>
            </div>

            {/* 操作按钮 */}
            <div className="flex gap-2 pt-1">
              <Button
                variant="outline"
                className="flex-1"
                onClick={handleReset}
              >
                <RefreshCcw className="h-4 w-4 mr-2" />
                再生成一个
              </Button>
              <Button
                variant="default"
                className="flex-1"
                onClick={() => handleOpenChange(false)}
              >
                完成
              </Button>
            </div>
          </div>
        )}

        {/* 使用说明 */}
        <div className="mt-2 rounded-lg bg-muted/50 border border-border/50 p-3 space-y-2">
          <p className="text-xs font-medium text-foreground">使用说明</p>
          <ol className="text-[11px] text-muted-foreground space-y-1.5 list-decimal list-inside">
            <li>在目标机器上下载并安装 NetsGo Agent</li>
            <li>使用上方的<strong>连接命令</strong>或<strong>快速链接</strong>启动 Agent</li>
            <li>Agent 将自动连接到此服务端并出现在面板中</li>
          </ol>
        </div>
      </DialogContent>
    </Dialog>
  );
}
