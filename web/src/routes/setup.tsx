import { useState, useCallback } from 'react';
import { createRoute, useNavigate } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useAuthStore } from '@/stores/auth-store';
import { api } from '@/lib/api';
import type { SetupResponse, PortRange } from '@/types';
import { Server, Globe, Shield, Check, Copy, Plus, X, Sparkles, ArrowRight, ArrowLeft, AlertTriangle } from 'lucide-react';
import { requireSetupPage } from '@/lib/auth';

export const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/setup',
  beforeLoad: requireSetupPage,
  component: SetupPage,
});

// 快捷端口预设
const PORT_PRESETS = [
  { label: 'Web 服务', range: { start: 8000, end: 9000 } },
  { label: '开发端口', range: { start: 3000, end: 3999 } },
  { label: '高端口', range: { start: 10000, end: 60000 } },
];

function SetupPage() {
  const [step, setStep] = useState(0);
  const navigate = useNavigate();
  const setAuth = useAuthStore((s) => s.setAuth);

  // Step 1: Admin
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [adminError, setAdminError] = useState('');

  // Step 2: Server address
  const [serverAddr, setServerAddr] = useState(() => {
    // 默认填入当前访问地址
    return window.location.origin;
  });
  const [addrError, setAddrError] = useState('');

  // Step 3: Ports
  const [ports, setPorts] = useState<PortRange[]>([]);
  const [portStart, setPortStart] = useState('');
  const [portEnd, setPortEnd] = useState('');
  const [portError, setPortError] = useState('');

  // Success
  const [loading, setLoading] = useState(false);
  const [submitError, setSubmitError] = useState('');
  const [result, setResult] = useState<SetupResponse | null>(null);
  const [copied, setCopied] = useState(false);

  // Validation
  const validateStep1 = useCallback(() => {
    if (!username.trim()) { setAdminError('用户名不能为空'); return false; }
    if (password.length < 8) { setAdminError('密码至少需要 8 个字符'); return false; }
    if (!/[a-zA-Z]/.test(password) || !/\d/.test(password)) { setAdminError('密码必须同时包含字母和数字'); return false; }
    if (password !== confirmPassword) { setAdminError('两次输入的密码不一致'); return false; }
    setAdminError('');
    return true;
  }, [username, password, confirmPassword]);

  const validateStep2 = useCallback(() => {
    if (!serverAddr.trim()) { setAddrError('请填写服务地址'); return false; }
    setAddrError('');
    return true;
  }, [serverAddr]);

  const addPort = useCallback(() => {
    const s = parseInt(portStart);
    const e = portEnd ? parseInt(portEnd) : s;
    if (isNaN(s) || s < 1 || s > 65535) { setPortError('起始端口无效'); return; }
    if (isNaN(e) || e < s || e > 65535) { setPortError('结束端口无效'); return; }
    // 检查重复
    for (const p of ports) {
      if (s <= p.end && e >= p.start) { setPortError('端口范围与已有规则重叠'); return; }
    }
    setPorts([...ports, { start: s, end: e }]);
    setPortStart('');
    setPortEnd('');
    setPortError('');
  }, [portStart, portEnd, ports]);

  const removePort = useCallback((index: number) => {
    setPorts(ports.filter((_, i) => i !== index));
  }, [ports]);

  const addPreset = useCallback((preset: { start: number; end: number }) => {
    // 检查是否已添加
    for (const p of ports) {
      if (p.start === preset.start && p.end === preset.end) return;
    }
    setPorts([...ports, preset]);
  }, [ports]);

  const totalPorts = ports.reduce((sum, p) => sum + (p.end - p.start + 1), 0);

  // 检测当前访问是否为不安全的方式 (HTTP / IP / 非标准端口)
  const currentOrigin = window.location.origin;
  const isInsecureAccess = (() => {
    try {
      const url = new URL(currentOrigin);
      const isHTTP = url.protocol === 'http:';
      const isIP = /^\d{1,3}(\.\d{1,3}){3}$/.test(url.hostname) || url.hostname === 'localhost' || url.hostname === '[::]' || url.hostname.startsWith('[');
      const hasPort = !!url.port;
      return isHTTP || isIP || hasPort;
    } catch {
      return false;
    }
  })();

  // Submit
  const handleSubmit = async () => {
    if (ports.length === 0) { setPortError('请至少添加一个端口规则'); return; }
    setLoading(true);
    setSubmitError('');
    try {
      const resp = await api.post<SetupResponse>('/api/setup/init', {
        admin: { username, password },
        server_addr: serverAddr,
        allowed_ports: ports,
      });
      setResult(resp);
      setAuth(resp.token, resp.user);
      setStep(3); // Success page
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : '初始化失败');
    } finally {
      setLoading(false);
    }
  };

  const handleCopy = async () => {
    if (result?.agent_key?.raw_key) {
      await navigator.clipboard.writeText(result.agent_key.raw_key);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const steps = [
    { icon: Server, label: '管理员账号' },
    { icon: Globe, label: '服务地址' },
    { icon: Shield, label: '端口白名单' },
  ];

  return (
    <div className="flex h-screen w-full items-center justify-center bg-background absolute inset-0 z-50">
      {/* Background decorations */}
      <div className="absolute top-0 left-0 w-[400px] h-[400px] bg-primary/5 rounded-full blur-3xl pointer-events-none -translate-x-1/2 -translate-y-1/2" />
      <div className="absolute bottom-0 right-0 w-[500px] h-[500px] bg-chart-2/5 rounded-full blur-3xl pointer-events-none translate-x-1/3 translate-y-1/3" />

      <div className="w-full max-w-lg mx-4">
        {/* Header */}
        <div className="text-center mb-8">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 bg-primary/10 rounded-full text-sm text-primary font-medium mb-4">
            <Sparkles className="w-4 h-4" /> 首次运行初始化
          </div>
          <h1 className="text-3xl font-bold tracking-tight">NetsGo 初始化向导</h1>
          <p className="text-muted-foreground mt-2">请完成以下配置以启用服务</p>
        </div>

        {/* Step indicators */}
        {step < 3 && (
          <div className="flex items-center justify-center gap-2 mb-8">
            {steps.map((s, i) => (
              <div key={i} className="flex items-center gap-2">
                <div className={`flex items-center gap-2 px-3 py-1.5 rounded-full text-xs font-medium transition-all ${
                  i === step
                    ? 'bg-primary text-primary-foreground shadow-lg shadow-primary/25'
                    : i < step
                      ? 'bg-primary/20 text-primary'
                      : 'bg-muted text-muted-foreground'
                }`}>
                  {i < step ? <Check className="w-3 h-3" /> : <s.icon className="w-3 h-3" />}
                  <span className="hidden sm:inline">{s.label}</span>
                </div>
                {i < steps.length - 1 && (
                  <div className={`w-8 h-px ${i < step ? 'bg-primary/40' : 'bg-border'}`} />
                )}
              </div>
            ))}
          </div>
        )}

        {/* Card */}
        <div className="rounded-2xl border border-border/50 bg-card shadow-xl relative overflow-hidden">
          <div className="absolute top-0 right-0 w-[250px] h-[250px] bg-primary/5 rounded-full blur-3xl pointer-events-none -translate-y-1/2 translate-x-1/2" />

          <div className="relative z-10 p-8">

            {/* Step 1: Admin */}
            {step === 0 && (
              <div className="space-y-6">
                <div className="flex items-center gap-3 mb-2">
                  <div className="p-2 bg-primary/10 rounded-lg">
                    <Server className="w-5 h-5 text-primary" />
                  </div>
                  <div>
                    <h2 className="text-lg font-semibold">设置管理员账号</h2>
                    <p className="text-sm text-muted-foreground">此账号用于登录管理后台</p>
                  </div>
                </div>

                <div className="space-y-4">
                  <div className="space-y-2">
                    <label className="text-sm font-medium">用户名</label>
                    <Input
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                      placeholder="admin"
                      autoComplete="username"
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm font-medium">密码</label>
                    <Input
                      type="password"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      placeholder="至少 8 位，包含字母和数字"
                      autoComplete="new-password"
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm font-medium">确认密码</label>
                    <Input
                      type="password"
                      value={confirmPassword}
                      onChange={(e) => setConfirmPassword(e.target.value)}
                      placeholder="再次输入密码"
                      autoComplete="new-password"
                    />
                  </div>
                </div>

                {adminError && (
                  <div className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">
                    <AlertTriangle className="w-4 h-4 shrink-0" />
                    {adminError}
                  </div>
                )}

                <div className="flex justify-end pt-2">
                  <Button onClick={() => { if (validateStep1()) setStep(1); }} className="gap-2">
                    下一步 <ArrowRight className="w-4 h-4" />
                  </Button>
                </div>
              </div>
            )}

            {/* Step 2: Server Address */}
            {step === 1 && (
              <div className="space-y-6">
                <div className="flex items-center gap-3 mb-2">
                  <div className="p-2 bg-primary/10 rounded-lg">
                    <Globe className="w-5 h-5 text-primary" />
                  </div>
                  <div>
                    <h2 className="text-lg font-semibold">服务管理端地址</h2>
                    <p className="text-sm text-muted-foreground">Agent 和用户访问此地址连接服务</p>
                  </div>
                </div>

                <div className="space-y-4">
                  <div className="space-y-2">
                    <label className="text-sm font-medium">服务地址</label>
                    <Input
                      value={serverAddr}
                      onChange={(e) => setServerAddr(e.target.value)}
                      placeholder="https://tunnel.example.com"
                    />
                  </div>
                </div>

                <div className="rounded-lg bg-chart-1/5 border border-chart-1/20 p-4 text-sm space-y-2">
                  <div className="flex items-start gap-2">
                    <span className="text-chart-1 mt-0.5">💡</span>
                    <div className="text-muted-foreground">
                      <p className="font-medium text-foreground mb-1">建议使用 HTTPS + 域名</p>
                      <p>这样可以确保控制通道的传输安全，防止 Agent Key 被中间人窃取。</p>
                      <p className="mt-1">如果暂时没有域名和证书，也可以使用如 <code className="px-1 py-0.5 bg-muted rounded text-xs">http://1.2.3.4:8080</code> 的形式。</p>
                    </div>
                  </div>
                </div>

                {isInsecureAccess && serverAddr === currentOrigin && (
                  <div className="rounded-lg bg-amber-500/10 border border-amber-500/30 p-4 text-sm space-y-2">
                    <div className="flex items-start gap-2">
                      <AlertTriangle className="w-4 h-4 text-amber-500 mt-0.5 shrink-0" />
                      <div className="text-muted-foreground">
                        <p className="font-medium text-amber-600 dark:text-amber-400 mb-1">检测到当前使用不安全方式访问</p>
                        <p>
                          当前地址为 <code className="px-1 py-0.5 bg-muted rounded text-xs">{currentOrigin}</code>，
                          {currentOrigin.startsWith('http:') && '使用了未加密的 HTTP 协议'}
                          {/\d{1,3}(\.\d{1,3}){3}/.test(currentOrigin) && '，且通过 IP 直接访问'}
                          。
                        </p>
                        <p className="mt-1">
                          生产环境中强烈建议配置域名并启用 HTTPS（如 <code className="px-1 py-0.5 bg-muted rounded text-xs">https://tunnel.example.com</code>），
                          以保证 Agent Key 等敏感信息的传输安全。
                        </p>
                      </div>
                    </div>
                  </div>
                )}

                {addrError && (
                  <div className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">
                    <AlertTriangle className="w-4 h-4 shrink-0" />
                    {addrError}
                  </div>
                )}

                <div className="flex justify-between pt-2">
                  <Button variant="outline" onClick={() => setStep(0)} className="gap-2">
                    <ArrowLeft className="w-4 h-4" /> 上一步
                  </Button>
                  <Button onClick={() => { if (validateStep2()) setStep(2); }} className="gap-2">
                    下一步 <ArrowRight className="w-4 h-4" />
                  </Button>
                </div>
              </div>
            )}

            {/* Step 3: Ports */}
            {step === 2 && (
              <div className="space-y-6">
                <div className="flex items-center gap-3 mb-2">
                  <div className="p-2 bg-primary/10 rounded-lg">
                    <Shield className="w-5 h-5 text-primary" />
                  </div>
                  <div>
                    <h2 className="text-lg font-semibold">允许穿透的端口</h2>
                    <p className="text-sm text-muted-foreground">只有白名单内的端口才能被隧道穿透</p>
                  </div>
                </div>

                {/* Quick presets */}
                <div className="space-y-2">
                  <label className="text-sm font-medium text-muted-foreground">快速预设</label>
                  <div className="flex flex-wrap gap-2">
                    {PORT_PRESETS.map((preset) => (
                      <button
                        key={preset.label}
                        onClick={() => addPreset(preset.range)}
                        className="px-3 py-1.5 text-xs font-medium bg-muted hover:bg-muted/80 rounded-full transition-colors border border-border/50 hover:border-primary/30"
                      >
                        {preset.label} ({preset.range.start}-{preset.range.end})
                      </button>
                    ))}
                  </div>
                </div>

                {/* Port list */}
                {ports.length > 0 && (
                  <div className="space-y-2">
                    <label className="text-sm font-medium text-muted-foreground">已添加的端口规则</label>
                    <div className="space-y-1.5 max-h-40 overflow-y-auto">
                      {ports.map((p, i) => (
                        <div key={i} className="flex items-center justify-between px-3 py-2 bg-muted/50 rounded-lg border border-border/30 group">
                          <div className="flex items-center gap-3 text-sm">
                            <span className="font-mono font-medium">
                              {p.start === p.end ? p.start : `${p.start} - ${p.end}`}
                            </span>
                            <span className="text-xs text-muted-foreground">
                              {p.start === p.end ? '单端口' : `${p.end - p.start + 1} 个端口`}
                            </span>
                          </div>
                          <button onClick={() => removePort(i)} className="text-muted-foreground hover:text-destructive transition-colors opacity-0 group-hover:opacity-100">
                            <X className="w-4 h-4" />
                          </button>
                        </div>
                      ))}
                    </div>
                    <div className="text-xs text-muted-foreground">
                      📊 当前共允许 <span className="font-medium text-foreground">{totalPorts.toLocaleString()}</span> 个端口
                    </div>
                  </div>
                )}

                {/* Add port */}
                <div className="space-y-2">
                  <label className="text-sm font-medium text-muted-foreground">添加端口规则</label>
                  <div className="flex items-center gap-2">
                    <Input
                      type="number"
                      placeholder="起始端口"
                      value={portStart}
                      onChange={(e) => setPortStart(e.target.value)}
                      min={1}
                      max={65535}
                      className="flex-1"
                    />
                    <span className="text-muted-foreground text-sm">-</span>
                    <Input
                      type="number"
                      placeholder="结束端口 (可选)"
                      value={portEnd}
                      onChange={(e) => setPortEnd(e.target.value)}
                      min={1}
                      max={65535}
                      className="flex-1"
                    />
                    <Button variant="outline" size="icon" onClick={addPort}>
                      <Plus className="w-4 h-4" />
                    </Button>
                  </div>
                </div>

                {portError && (
                  <div className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">
                    <AlertTriangle className="w-4 h-4 shrink-0" />
                    {portError}
                  </div>
                )}

                {submitError && (
                  <div className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">
                    <AlertTriangle className="w-4 h-4 shrink-0" />
                    {submitError}
                  </div>
                )}

                <div className="flex justify-between pt-2">
                  <Button variant="outline" onClick={() => setStep(1)} className="gap-2">
                    <ArrowLeft className="w-4 h-4" /> 上一步
                  </Button>
                  <Button onClick={handleSubmit} disabled={loading} className="gap-2">
                    {loading ? '初始化中...' : <>🚀 完成初始化</>}
                  </Button>
                </div>
              </div>
            )}

            {/* Success */}
            {step === 3 && result && (
              <div className="space-y-6 text-center">
                <div className="flex flex-col items-center gap-3">
                  <div className="w-16 h-16 bg-green-500/10 rounded-full flex items-center justify-center">
                    <Check className="w-8 h-8 text-green-500" />
                  </div>
                  <h2 className="text-2xl font-bold">🎉 初始化成功！</h2>
                  <p className="text-sm text-muted-foreground">NetsGo 服务已准备就绪</p>
                </div>

                {result.agent_key && (
                  <div className="rounded-xl border border-chart-1/30 bg-chart-1/5 p-5 text-left space-y-3">
                    <div className="flex items-center gap-2 text-sm font-medium">
                      <Shield className="w-4 h-4 text-chart-1" />
                      您的首个 Agent Key（仅显示一次！）
                    </div>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 px-3 py-2 bg-background rounded-lg text-sm font-mono break-all border border-border/50">
                        {result.agent_key.raw_key}
                      </code>
                      <Button variant="outline" size="icon" onClick={handleCopy} className="shrink-0">
                        {copied ? <Check className="w-4 h-4 text-green-500" /> : <Copy className="w-4 h-4" />}
                      </Button>
                    </div>
                    <div className="flex items-start gap-2 text-xs text-muted-foreground">
                      <AlertTriangle className="w-3.5 h-3.5 text-chart-1 mt-0.5 shrink-0" />
                      <span>请立即复制并妥善保管此 Key，关闭此页面后无法再次查看！</span>
                    </div>
                  </div>
                )}

                <Button onClick={() => navigate({ to: '/dashboard' })} className="w-full gap-2" size="lg">
                  进入管理面板 <ArrowRight className="w-4 h-4" />
                </Button>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
