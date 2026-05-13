import { Toaster } from "react-hot-toast";
import { ActionFooter, BrandHeader } from "./AppShell";
import { ConnectedView, ConnectingView, NewConnectionForm, SavedConnectionView } from "./ConnectionViews";
import { useClientConnection } from "./useClientConnection";
import "./App.css";

function App() {
  const connection = useClientConnection();

  return (
    <div className="app-wrapper">
      <Toaster position="top-right" toastOptions={{ duration: 1800 }} />

      <BrandHeader />

      <main className="content-area">
        {connection.state === "connected" ? (
          <ConnectedView server={connection.visibleServer} />
        ) : connection.state === "saved" ? (
          <SavedConnectionView server={connection.lastServer ?? ""} />
        ) : connection.state === "connecting" || connection.state === "disconnecting" ? (
          <ConnectingView statusText={connection.statusText} server={connection.visibleConnectingServer} />
        ) : (
          <NewConnectionForm
            server={connection.server}
            setServer={connection.setServer}
            accessKey={connection.key}
            setAccessKey={connection.setKey}
          />
        )}
      </main>

      <ActionFooter
        state={connection.state}
        canConnect={connection.canConnect}
        canReconnect={connection.canReconnect}
        onConnect={connection.connect}
        onDisconnect={() => void connection.disconnect()}
        onReconnect={connection.reconnect}
        onForgetConnection={connection.forgetConnection}
      />
    </div>
  );
}

export default App;
