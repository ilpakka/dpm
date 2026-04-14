//! Tokio-based JSON-RPC 2.0 client over `dpm serve --stdio`.
//!
//! The client spawns the Go binary as a child process, hands its stdin/stdout
//! to a reader/writer task pair, and lets the rest of the app talk to it via
//! `request(...) -> JsonValue` futures and a `Notification` mpsc channel.

use std::collections::HashMap;
use std::process::Stdio;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Arc;
use std::time::Duration;

use anyhow::{anyhow, Context, Result};
use serde::Deserialize;
use serde_json::{json, Value};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::process::{Child, ChildStdin, Command};
use tokio::sync::{mpsc, oneshot, Mutex};
use tokio::time::timeout;

/// Maximum time to wait for any single RPC response.
/// Long-running operations (install, cargo compile) can legitimately take
/// several minutes; 10 minutes is chosen to be safely above the longest
/// backend timeout (30 min cargo) while still terminating eventually.
const REQUEST_TIMEOUT: Duration = Duration::from_secs(600);

/// One server-initiated notification.
#[derive(Debug, Clone)]
pub enum Notification {
    /// `log` — single line of engine output.
    Log(String),
    /// Any other unknown notification, kept as raw JSON for forward compat.
    #[allow(dead_code)]
    Other { method: String, params: Value },
}

#[derive(Debug, Deserialize)]
struct WireMessage {
    #[serde(default)]
    id: Option<Value>,
    #[serde(default)]
    result: Option<Value>,
    #[serde(default)]
    error: Option<WireError>,
    #[serde(default)]
    method: Option<String>,
    #[serde(default)]
    params: Option<Value>,
}

#[derive(Debug, Deserialize)]
struct WireError {
    code: i64,
    message: String,
}

type Pending = Arc<Mutex<HashMap<i64, oneshot::Sender<Result<Value>>>>>;

/// JSON-RPC client tied to a child `dpm serve --stdio` process.
pub struct RpcClient {
    next_id: AtomicI64,
    stdin: Mutex<ChildStdin>,
    pending: Pending,
    _child: Mutex<Option<Child>>,
}

impl RpcClient {
    /// Spawn `dpm serve --stdio` and start the reader task.
    ///
    /// Returns the client and an `mpsc::Receiver` for server-initiated
    /// notifications. The reader task lives until the child exits or the
    /// receiver is dropped.
    pub async fn spawn(dpm_binary: &str) -> Result<(Arc<Self>, mpsc::Receiver<Notification>)> {
        let mut child = Command::new(dpm_binary)
            .arg("serve")
            .arg("--stdio")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .with_context(|| format!("spawn {}", dpm_binary))?;

        let stdin = child
            .stdin
            .take()
            .ok_or_else(|| anyhow!("failed to capture stdin of {}", dpm_binary))?;
        let stdout = child
            .stdout
            .take()
            .ok_or_else(|| anyhow!("failed to capture stdout of {}", dpm_binary))?;

        let pending: Pending = Arc::new(Mutex::new(HashMap::new()));
        let (notif_tx, notif_rx) = mpsc::channel::<Notification>(256);

        // Spawn the reader task.
        let pending_reader = pending.clone();
        tokio::spawn(async move {
            let mut reader = BufReader::new(stdout).lines();
            while let Ok(Some(line)) = reader.next_line().await {
                if line.is_empty() {
                    continue;
                }
                let msg: WireMessage = match serde_json::from_str(&line) {
                    Ok(m) => m,
                    Err(_) => continue,
                };

                // Notification: no id.
                if msg.id.is_none() {
                    if let Some(method) = msg.method {
                        let params = msg.params.unwrap_or(Value::Null);
                        let notif = match method.as_str() {
                            "log" => {
                                let line = params
                                    .get("line")
                                    .and_then(|v| v.as_str())
                                    .unwrap_or("")
                                    .to_string();
                                Notification::Log(line)
                            }
                            _ => Notification::Other { method, params },
                        };
                        // Drop on overflow rather than blocking the reader.
                        let _ = notif_tx.try_send(notif);
                    }
                    continue;
                }

                // Response: id is set.
                let id = msg.id.as_ref().and_then(|v| v.as_i64());
                if let Some(id) = id {
                    let mut pending = pending_reader.lock().await;
                    if let Some(tx) = pending.remove(&id) {
                        let result = if let Some(err) = msg.error {
                            Err(anyhow!("rpc error {}: {}", err.code, err.message))
                        } else {
                            Ok(msg.result.unwrap_or(Value::Null))
                        };
                        let _ = tx.send(result);
                    }
                }
            }
            // Reader exited — fail any in-flight requests.
            let mut pending = pending_reader.lock().await;
            for (_, tx) in pending.drain() {
                let _ = tx.send(Err(anyhow!("dpm serve exited unexpectedly")));
            }
        });

        let client = Arc::new(Self {
            next_id: AtomicI64::new(1),
            stdin: Mutex::new(stdin),
            pending,
            _child: Mutex::new(Some(child)),
        });

        Ok((client, notif_rx))
    }

    /// Send a request and wait for the response.
    pub async fn request(&self, method: &str, params: Value) -> Result<Value> {
        let id = self.next_id.fetch_add(1, Ordering::SeqCst);
        let req = json!({
            "jsonrpc": "2.0",
            "id": id,
            "method": method,
            "params": params,
        });

        let (tx, rx) = oneshot::channel();
        {
            let mut pending = self.pending.lock().await;
            pending.insert(id, tx);
        }

        // Write request as one line.
        {
            let mut stdin = self.stdin.lock().await;
            let mut bytes = serde_json::to_vec(&req)?;
            bytes.push(b'\n');
            stdin.write_all(&bytes).await?;
            stdin.flush().await?;
        }

        match timeout(REQUEST_TIMEOUT, rx).await {
            Ok(Ok(result)) => result,
            Ok(Err(_)) => Err(anyhow!("response channel closed")),
            Err(_elapsed) => {
                // Timed out — remove the pending entry so the sender is dropped
                // rather than sitting in the map until the process exits.
                self.pending.lock().await.remove(&id);
                Err(anyhow!(
                    "rpc: request '{}' timed out after {}s",
                    method,
                    REQUEST_TIMEOUT.as_secs()
                ))
            }
        }
    }

    /// Convenience: request and deserialize into T.
    pub async fn call<T: for<'de> Deserialize<'de>>(
        &self,
        method: &str,
        params: Value,
    ) -> Result<T> {
        let raw = self.request(method, params).await?;
        Ok(serde_json::from_value(raw)?)
    }

    /// Convenience: request that returns no result.
    pub async fn call_void(&self, method: &str, params: Value) -> Result<()> {
        self.request(method, params).await?;
        Ok(())
    }
}
