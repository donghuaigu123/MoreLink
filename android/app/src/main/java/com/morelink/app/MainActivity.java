package com.morelink.app;

import android.Manifest;
import android.app.Activity;
import android.content.BroadcastReceiver;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.provider.Settings;
import android.text.TextUtils;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

/** Main UI: status, pairing, peers, clipboard send, permissions, notification access. */
public class MainActivity extends Activity {
    private TextView statusView, peersView, infoView;
    private EditText pinEdit, targetIpEdit, clipEdit;
    private final BroadcastReceiver receiver = new BroadcastReceiver() {
        @Override
        public void onReceive(Context c, Intent i) { refresh(); }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setTitle("MoreLink · 安卓");
        SharedPreferences p = getSharedPreferences(LinkService.PREFS, MODE_PRIVATE);
        if (p.getString("name", "").isEmpty()) {
            p.edit().putString("name", android.os.Build.MODEL).apply();
        }

        ScrollView scroll = new ScrollView(this);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        int pad = dp(16);
        root.setPadding(pad, pad, pad, pad);
        scroll.addView(root, new ScrollView.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT));

        infoView = new TextView(this);
        infoView.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13);
        infoView.setTextColor(0xFF6EE7B7);
        root.addView(infoView);

        statusView = new TextView(this);
        statusView.setTextSize(TypedValue.COMPLEX_UNIT_SP, 15);
        statusView.setPadding(0, dp(8), 0, dp(8));
        root.addView(statusView);

        root.addView(section("配对"));
        TextView hint = new TextView(this);
        hint.setText("在电脑上点“配对”后输入这里生成的 6 位码；或把电脑上显示的码填入下方完成配对。");
        hint.setTextSize(TypedValue.COMPLEX_UNIT_SP, 12);
        root.addView(hint);

        Button genPin = new Button(this);
        genPin.setText("生成配对码");
        genPin.setOnClickListener(v -> {
            String pin = LinkService.newPin(this);
            pinEdit.setText(pin);
            Toast.makeText(this, "配对码已生成，请在电脑端输入", Toast.LENGTH_LONG).show();
        });
        root.addView(genPin);

        pinEdit = new EditText(this);
        pinEdit.setHint("6 位配对码（电脑端生成时填这里）");
        pinEdit.setInputType(android.text.InputType.TYPE_CLASS_NUMBER);
        root.addView(pinEdit);

        Button doPair = new Button(this);
        doPair.setText("保存配对码");
        doPair.setOnClickListener(v -> {
            String pin = pinEdit.getText().toString().trim();
            if (pin.length() != 6) { toast("请输入 6 位数字"); return; }
            SharedPreferences pref = getSharedPreferences(LinkService.PREFS, MODE_PRIVATE);
            pref.edit().putString("pin", pin).putBoolean("paired", true).apply();
            String peer = PeerStore.firstWindowsIp(this) != null ? "windows-peer" : "windows-peer";
            pref.edit().putString("peer", peer).apply();
            // Recompute key with the peer id we know; if unknown, connection handshake re-derives.
            toast("配对码已保存");
            refresh();
        });
        root.addView(doPair);

        root.addView(section("附近设备"));
        peersView = new TextView(this);
        peersView.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13);
        peersView.setText("扫描中…");
        root.addView(peersView);

        root.addView(section("发送剪贴板"));
        targetIpEdit = new EditText(this);
        targetIpEdit.setHint("电脑 IP（点击设备列表自动填入）");
        root.addView(targetIpEdit);
        clipEdit = new EditText(this);
        clipEdit.setHint("要发送的文本");
        root.addView(clipEdit);
        Button send = new Button(this);
        send.setText("发送文本到电脑");
        send.setOnClickListener(v -> {
            String ip = targetIpEdit.getText().toString().trim();
            String text = clipEdit.getText().toString();
            if (ip.isEmpty()) { toast("请填写电脑 IP"); return; }
            if (TextUtils.isEmpty(text)) { toast("请输入文本"); return; }
            String r = LinkService.sendClipboard(this, ip, Mlp.DATA_PORT, text);
            toast(r);
        });
        root.addView(send);

        root.addView(section("权限与后台"));
        Button notifAccess = new Button(this);
        notifAccess.setText("开启通知读取权限");
        notifAccess.setOnClickListener(v -> requestNotificationAccess());
        root.addView(notifAccess);

        Button start = new Button(this);
        start.setText("启动 / 保持后台运行");
        start.setOnClickListener(v -> startService());
        root.addView(start);

        TextView foot = new TextView(this);
        foot.setText("MoreLink v1.0.0 · 原创开源项目\n局域网自动发现，端到端 AES-256-GCM 加密");
        foot.setTextSize(TypedValue.COMPLEX_UNIT_SP, 11);
        foot.setTextColor(0xFF8B98AD);
        foot.setPadding(0, dp(16), 0, 0);
        root.addView(foot);

        setContentView(scroll);
        registerReceiver(receiver, new IntentFilter(LinkService.ACTION_STATE));
        startService();
        requestPermissions();
        refresh();
    }

    private TextView section(String title) {
        TextView t = new TextView(this);
        t.setText("── " + title + " ──");
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, 14);
        t.setGravity(Gravity.CENTER);
        t.setPadding(0, dp(14), 0, dp(6));
        t.setTextColor(0xFF6EE7B7);
        return t;
    }

    private void startService() {
        Intent i = new Intent(this, LinkService.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) startForegroundService(i);
        else startService(i);
    }

    private void requestPermissions() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED)
                requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 1);
        }
    }

    private void requestNotificationAccess() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP_MR1) {
            try {
                startActivity(new Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS));
            } catch (Exception e) {
                toast("请到 设置 → 通知使用权 中开启 MoreLink");
            }
        } else {
            startActivity(new Intent("android.settings.ACTION_NOTIFICATION_LISTENER_SETTINGS"));
        }
    }

    private void refresh() {
        if (statusView == null) return;
        String json = LinkService.stateJson(this);
        try {
            JSONObject o = new JSONObject(json);
            String id = o.optString("id");
            boolean paired = o.optBoolean("paired");
            statusView.setText("状态: " + o.optString("status") + "\n已配对: " + (paired ? "是" : "否") +
                    " · 通知: " + o.optInt("notifications") + " · 文件: " + o.optInt("files"));
        } catch (Exception ignored) {}

        SharedPreferences p = getSharedPreferences(LinkService.PREFS, MODE_PRIVATE);
        infoView.setText("设备名: " + p.getString("name", "") + "\nID: " + p.getString("id", ""));

        String peers = PeerStore.peersJson(this);
        StringBuilder sb = new StringBuilder();
        try {
            JSONArray arr = new JSONArray(peers);
            for (int i = 0; i < arr.length(); i++) {
                JSONObject o = arr.getJSONObject(i);
                String name = o.optString("name");
                String plat = o.optString("platform");
                String ip = o.optString("ip");
                sb.append("• ").append(name).append(" [").append(plat).append("] ").append(ip).append("\n");
            }
        } catch (Exception ignored) {}
        peersView.setText(sb.length() == 0 ? "暂未发现设备" : sb.toString());

        // Auto-fill IP if only one peer.
        try {
            JSONArray arr = new JSONArray(peers);
            if (arr.length() == 1 && targetIpEdit.getText().toString().isEmpty()) {
                targetIpEdit.setText(arr.getJSONObject(0).optString("ip"));
            }
        } catch (Exception ignored) {}
    }

    private void toast(String s) { Toast.makeText(this, s, Toast.LENGTH_SHORT).show(); }
    private int dp(int v) { return (int) TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v,
            getResources().getDisplayMetrics()); }

    @Override
    protected void onDestroy() {
        try { unregisterReceiver(receiver); } catch (Exception ignored) {}
        super.onDestroy();
    }
}
