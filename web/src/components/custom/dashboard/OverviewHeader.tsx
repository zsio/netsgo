

export function OverviewHeader() {
  return (
    <div className="flex flex-col gap-2">
      <h1 className="flex items-center gap-2 text-xl font-bold tracking-tight text-foreground sm:text-2xl">
        Dashboard
      </h1>
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        实时查看服务端运行状态、Client 连接分布以及所有网络隧道的健康状况。
      </p>
    </div>
  );
}
