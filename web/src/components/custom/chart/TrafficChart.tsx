export function TrafficChart() {
  return (
    <div className="rounded-xl border border-border/40 bg-card/30 backdrop-blur-sm shadow-sm p-6 relative overflow-hidden group">
      <div className="flex items-center justify-between mb-6 relative z-10">
        <h3 className="font-semibold text-lg">📊 流量趋势</h3>
        <div className="flex gap-2">
          <span className="text-xs bg-muted px-2 py-1 rounded text-muted-foreground cursor-pointer hover:text-foreground">1h</span>
          <span className="text-xs bg-muted px-2 py-1 rounded text-muted-foreground cursor-pointer hover:text-foreground">24h</span>
          <span className="text-xs bg-primary/20 text-primary px-2 py-1 rounded cursor-pointer">7d</span>
        </div>
      </div>

      <div className="h-48 w-full border-b border-l border-border/50 relative z-10 flex items-end">
        {/* Placeholder chart lines */}
        <svg className="w-full h-full text-primary" preserveAspectRatio="none" viewBox="0 0 100 100">
          <path d="M0,100 L0,50 C20,60 30,20 50,40 C70,60 80,10 100,30 L100,100 Z" fill="currentColor" fillOpacity="0.1" />
          <path d="M0,50 C20,60 30,20 50,40 C70,60 80,10 100,30" fill="none" stroke="currentColor" strokeWidth="2" strokeOpacity="0.8" />
        </svg>
        <svg className="absolute top-0 left-0 w-full h-full text-emerald-500" preserveAspectRatio="none" viewBox="0 0 100 100">
          <path d="M0,100 L0,80 C20,70 30,90 50,70 C70,50 80,80 100,60 L100,100 Z" fill="currentColor" fillOpacity="0.1" />
          <path d="M0,80 C20,70 30,90 50,70 C70,50 80,80 100,60" fill="none" stroke="currentColor" strokeWidth="2" strokeOpacity="0.8" />
        </svg>
      </div>
      <div className="absolute inset-0 bg-gradient-to-t from-background/80 to-transparent pointer-events-none" />
    </div>
  );
}
