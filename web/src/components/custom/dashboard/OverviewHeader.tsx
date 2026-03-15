

export function OverviewHeader() {
  return (
    <div className="flex flex-col gap-2">
      <h1 className="text-2xl font-bold tracking-tight text-foreground flex items-center gap-2">
        Dashboard
      </h1>
      <p className="text-muted-foreground text-sm flex items-center gap-2">
        实时查看服务端运行状态、Client 连接分布以及所有网络隧道的健康状况。
      </p>
    </div>
  );
}
