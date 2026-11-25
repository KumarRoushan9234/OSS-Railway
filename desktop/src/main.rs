// OSS Railway desktop: manage any number of OSS Railway servers (a VPS anywhere, or a box at home
// behind a tunnel) from your computer.
//
// - Each server is a URL plus an API token. Tokens live in the OS keychain (Windows Credential
//   Manager, macOS Keychain, Secret Service on Linux), never in a file or in the page's JavaScript.
// - Every request to a server is made here in Rust, so the panel needs no CORS and the token never
//   reaches the web view.
// - "Analyze" sends a service's recent logs to an AI model on *this* computer (Ollama by default),
//   so a remote server's logs are analysed at home without a cloud AI.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::{fs, path::PathBuf, time::Duration};
use tauri::{AppHandle, Manager, WebviewUrl, WebviewWindowBuilder};

const KEYRING_SERVICE: &str = "oss-railway-desktop";

#[derive(Serialize, Deserialize, Clone)]
struct Server {
    id: String,
    name: String,
    url: String,
}

#[derive(Serialize, Deserialize, Clone)]
struct Settings {
    ai_url: String,
    ai_model: String,
}

impl Default for Settings {
    fn default() -> Self {
        Settings { ai_url: "http://localhost:11434/v1".into(), ai_model: "qwen2.5:7b".into() }
    }
}

fn data_file(app: &AppHandle, name: &str) -> Result<PathBuf, String> {
    let dir = app.path().app_data_dir().map_err(|e| e.to_string())?;
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    Ok(dir.join(name))
}

fn load<T: for<'de> Deserialize<'de> + Default>(app: &AppHandle, name: &str) -> T {
    data_file(app, name)
        .ok()
        .and_then(|p| fs::read(p).ok())
        .and_then(|b| serde_json::from_slice(&b).ok())
        .unwrap_or_default()
}

fn save<T: Serialize>(app: &AppHandle, name: &str, v: &T) -> Result<(), String> {
    let p = data_file(app, name)?;
    fs::write(p, serde_json::to_vec_pretty(v).map_err(|e| e.to_string())?).map_err(|e| e.to_string())
}

fn servers(app: &AppHandle) -> Vec<Server> {
    load(app, "servers.json")
}

fn server(app: &AppHandle, id: &str) -> Result<Server, String> {
    servers(app).into_iter().find(|s| s.id == id).ok_or_else(|| "unknown server".into())
}

fn token(id: &str) -> Result<String, String> {
    keyring::Entry::new(KEYRING_SERVICE, id)
        .and_then(|e| e.get_password())
        .map_err(|e| format!("token not found in the keychain: {e}"))
}

/// Accepts https:// anywhere, and http:// only for this computer or a private network address
/// (sending a token over plain http on the internet would expose it).
fn normalize_url(raw: &str) -> Result<String, String> {
    let u = url::Url::parse(raw.trim()).map_err(|_| "enter a full address, like https://panel.example.com")?;
    let host = u.host_str().unwrap_or_default().to_string();
    let local = host == "localhost"
        || host.ends_with(".localhost")
        || host.parse::<std::net::IpAddr>().map(|ip| ip.is_loopback() || matches!(ip, std::net::IpAddr::V4(v4) if v4.is_private())).unwrap_or(false);
    match u.scheme() {
        "https" => {}
        "http" if local => {}
        _ => return Err("use https:// (http is only allowed for this computer or your home network)".into()),
    }
    Ok(format!("{}://{}", u.scheme(), u.host_str().unwrap_or_default()) + &u.port().map(|p| format!(":{p}")).unwrap_or_default())
}

fn client() -> reqwest::Client {
    reqwest::Client::builder().timeout(Duration::from_secs(30)).build().expect("http client")
}

async fn call(base: &str, token: &str, method: reqwest::Method, path: &str, body: Option<Value>) -> Result<Value, String> {
    // Only the panel's API, and no path tricks.
    if !path.starts_with("/api/") || path.contains("..") {
        return Err("bad path".into());
    }
    let mut req = client().request(method, format!("{base}{path}")).bearer_auth(token);
    if let Some(b) = body {
        req = req.json(&b);
    }
    let res = req.send().await.map_err(|e| format!("could not reach the server: {e}"))?;
    let status = res.status();
    let text = res.text().await.unwrap_or_default();
    let v: Value = serde_json::from_str(&text).unwrap_or(Value::Null);
    if !status.is_success() {
        let msg = v.get("error").and_then(|e| e.as_str()).unwrap_or(status.as_str()).to_string();
        return Err(msg);
    }
    Ok(v)
}

#[tauri::command]
fn list_servers(app: AppHandle) -> Vec<Server> {
    servers(&app)
}

/// Checks the address and token by listing services, then saves the server.
#[tauri::command]
async fn add_server(app: AppHandle, name: String, url: String, token: String) -> Result<Server, String> {
    let base = normalize_url(&url)?;
    let token = token.trim().to_string();
    if !token.starts_with("ossr_") {
        return Err("paste an API token from the server's Settings → API tokens (it starts with ossr_)".into());
    }
    call(&base, &token, reqwest::Method::GET, "/api/services", None).await?;
    let name = if name.trim().is_empty() { base.split("://").nth(1).unwrap_or(&base).to_string() } else { name.trim().to_string() };
    let s = Server { id: format!("srv-{}", std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_millis()), name, url: base };
    keyring::Entry::new(KEYRING_SERVICE, &s.id)
        .and_then(|e| e.set_password(&token))
        .map_err(|e| format!("could not save the token in the keychain: {e}"))?;
    let mut all = servers(&app);
    all.push(s.clone());
    save(&app, "servers.json", &all)?;
    Ok(s)
}

#[tauri::command]
fn remove_server(app: AppHandle, id: String) -> Result<(), String> {
    if let Ok(e) = keyring::Entry::new(KEYRING_SERVICE, &id) {
        let _ = e.delete_credential();
    }
    let all: Vec<Server> = servers(&app).into_iter().filter(|s| s.id != id).collect();
    save(&app, "servers.json", &all)
}

/// GET any /api/ path on a server, e.g. /api/services or /api/server.
#[tauri::command]
async fn api_get(app: AppHandle, id: String, path: String) -> Result<Value, String> {
    let s = server(&app, &id)?;
    call(&s.url, &token(&id)?, reqwest::Method::GET, &path, None).await
}

/// Deploy or restart a service (needs a full-access token).
#[tauri::command]
async fn service_action(app: AppHandle, id: String, service: i64, action: String) -> Result<Value, String> {
    if action != "deploy" && action != "restart" {
        return Err("unknown action".into());
    }
    let s = server(&app, &id)?;
    call(&s.url, &token(&id)?, reqwest::Method::POST, &format!("/api/services/{service}/{action}"), Some(json!({}))).await
}

/// Opens the server's full dashboard in its own window (it signs in there with its own session).
#[tauri::command]
fn open_dashboard(app: AppHandle, id: String) -> Result<(), String> {
    let s = server(&app, &id)?;
    let url = s.url.parse().map_err(|_| "bad url")?;
    let label = format!("dash-{}", id.replace(|c: char| !c.is_ascii_alphanumeric(), ""));
    if let Some(w) = app.get_webview_window(&label) {
        return w.set_focus().map_err(|e| e.to_string());
    }
    WebviewWindowBuilder::new(&app, label, WebviewUrl::External(url))
        .title(format!("{} — OSS Railway", s.name))
        .inner_size(1280.0, 840.0)
        .build()
        .map(|_| ())
        .map_err(|e| e.to_string())
}

#[tauri::command]
fn get_settings(app: AppHandle) -> Settings {
    load(&app, "settings.json")
}

#[tauri::command]
fn set_settings(app: AppHandle, settings: Settings) -> Result<(), String> {
    url::Url::parse(&settings.ai_url).map_err(|_| "AI address must be a URL like http://localhost:11434/v1")?;
    save(&app, "settings.json", &settings)
}

/// Fetches a service's recent logs from the server and asks the local AI model what's wrong.
#[tauri::command]
async fn analyze(app: AppHandle, id: String, service: i64) -> Result<String, String> {
    let s = server(&app, &id)?;
    let t = token(&id)?;
    let svc = call(&s.url, &t, reqwest::Method::GET, &format!("/api/services/{service}"), None).await?;
    let deps = call(&s.url, &t, reqwest::Method::GET, &format!("/api/services/{service}/deployments"), None).await?;
    let logs = call(&s.url, &t, reqwest::Method::GET, &format!("/api/logs/search?service={service}"), None).await?;
    let mut lines: Vec<String> = logs.as_array().map(|a| a.iter().filter_map(|h| h["line"].as_str().map(String::from)).collect()).unwrap_or_default();
    lines.reverse(); // the API returns newest first
    let last_deploy = deps.get(0).cloned().unwrap_or(Value::Null);
    let prompt = format!(
        "Service: {} ({}), status {}.\nLatest deployment: {} — {}\n\nRecent log lines:\n{}",
        svc["name"], svc["source"], svc["status"], last_deploy["status"], last_deploy["message"],
        if lines.is_empty() { "(none stored yet)".into() } else { lines.join("\n") }
    );
    let cfg: Settings = load(&app, "settings.json");
    let body = json!({
        "model": cfg.ai_model,
        "temperature": 0.2,
        "stream": false,
        "messages": [
            {"role": "system", "content": "You help people run apps on their own server with OSS Railway (Docker + Caddy). From the service details and logs, say in under 200 words what went wrong (quote the log line) and the most likely fix as numbered steps. If the logs look healthy, say so. Never invent log lines."},
            {"role": "user", "content": prompt}
        ]
    });
    let res = reqwest::Client::builder()
        .timeout(Duration::from_secs(180))
        .build()
        .map_err(|e| e.to_string())?
        .post(format!("{}/chat/completions", cfg.ai_url.trim_end_matches('/')))
        .json(&body)
        .send()
        .await
        .map_err(|e| format!("could not reach the AI model at {}: {e}. Is Ollama running?", cfg.ai_url))?;
    let v: Value = res.json().await.map_err(|e| e.to_string())?;
    v["choices"][0]["message"]["content"]
        .as_str()
        .map(|s| s.trim().to_string())
        .ok_or_else(|| format!("unexpected reply from the AI model: {}", v.get("error").unwrap_or(&v)))
}

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            list_servers, add_server, remove_server, api_get, service_action, open_dashboard, get_settings, set_settings, analyze
        ])
        .run(tauri::generate_context!())
        .expect("error while running OSS Railway desktop");
}

#[cfg(test)]
mod tests {
    use super::normalize_url;

    #[test]
    fn urls() {
        assert_eq!(normalize_url("https://panel.example.com/some/path").unwrap(), "https://panel.example.com");
        assert_eq!(normalize_url("http://localhost:5176").unwrap(), "http://localhost:5176");
        assert_eq!(normalize_url("http://192.168.1.20").unwrap(), "http://192.168.1.20");
        assert!(normalize_url("http://panel.example.com").is_err(), "plain http on the internet would leak the token");
        assert!(normalize_url("ftp://x").is_err());
        assert!(normalize_url("not a url").is_err());
    }
}

/// Against a running panel: `OSSR_TEST_URL=http://localhost OSSR_TEST_TOKEN=ossr_… cargo test -- --ignored`
#[cfg(test)]
mod live {
    use super::*;

    #[tokio::test]
    #[ignore]
    async fn reads_a_real_panel() {
        let url = std::env::var("OSSR_TEST_URL").expect("OSSR_TEST_URL");
        let token = std::env::var("OSSR_TEST_TOKEN").expect("OSSR_TEST_TOKEN");
        let base = normalize_url(&url).unwrap();
        let services = call(&base, &token, reqwest::Method::GET, "/api/services", None).await.unwrap();
        assert!(services.as_array().map(|a| !a.is_empty()).unwrap_or(false), "no services: {services}");
        let server = call(&base, &token, reqwest::Method::GET, "/api/server", None).await.unwrap();
        assert!(server["host"]["cpus"].as_i64().unwrap_or(0) > 0);
        assert!(call(&base, &token, reqwest::Method::GET, "/api/../etc", None).await.is_err(), "path tricks must be refused");
        let err = call(&base, &token, reqwest::Method::POST, "/api/services/1/deploy", Some(json!({}))).await.unwrap_err();
        assert!(err.contains("read-only"), "read-only token deployed: {err}");
        assert!(call(&base, "ossr_wrong", reqwest::Method::GET, "/api/services", None).await.is_err());
    }
}
