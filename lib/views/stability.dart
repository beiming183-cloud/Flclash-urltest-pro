import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/core/core.dart';
import 'package:fl_clash/widgets/widgets.dart';
import 'package:flutter/material.dart';

String stabilityStatusText(BuildContext context, String status) {
  final l10n = context.appLocalizations;
  return switch (status) {
    'ready' => l10n.stabilityReady,
    'stopped' => l10n.stabilityStopped,
    'unexpected_exit' => l10n.stabilityWrongExit,
    'exit_query_timeout' => l10n.stabilityExitTimeout,
    'cert_untrusted' || 'cert_hostname' || 'cert_expired' => l10n.stabilityCertificate,
    'tls_aborted' || 'tls_handshake_timeout' => l10n.stabilityTlsTimeout,
    'https_timeout' || 'relay_dial_timeout' => l10n.stabilityDelayTimeout,
    'residential_auth' || 'connect_rejected' => l10n.stabilityAuthentication,
    'budget_exhausted' => l10n.stabilityBudget,
    _ => l10n.stabilityValidating,
  };
}

Future<void> exportStabilityDiagnostics() async {
  final data = await coreController.exportStabilityDiagnostics();
  if (data == null) throw StateError('diagnostic export unavailable');
  final bytes = base64Decode(data['base64'] as String);
  if (bytes.length > 2 * 1024 * 1024 || bytes.length != data['size']) {
    throw StateError('invalid diagnostic archive');
  }
  final file = File('${await appPath.tempPath}/stability-diagnostic.zip');
  try {
    await file.writeAsBytes(bytes, flush: true);
    await picker.saveFileWithPath('FlClash-stability-diagnostic.zip', file.path);
  } finally {
    if (await file.exists()) await file.delete();
  }
}

class StabilityView extends StatefulWidget {
  const StabilityView({super.key});
  @override
  State<StabilityView> createState() => _StabilityViewState();
}

class _StabilityViewState extends State<StabilityView> {
  Timer? _timer;
  List<dynamic> _groups = [];
  bool _reading = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    unawaited(_refresh());
    _timer = Timer.periodic(const Duration(seconds: 3), (_) => unawaited(_refresh()));
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    if (_reading) return;
    _reading = true;
    try {
      final groups = await coreController.getStabilityState();
      if (mounted) setState(() { _groups = groups; _error = null; });
    } catch (_) {
      if (mounted) setState(() => _error = context.appLocalizations.stabilityUnavailable);
    } finally {
      _reading = false;
    }
  }

  Future<void> _action(Future<void> Function() action) async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await action();
      await _refresh();
    } catch (_) {
      if (mounted) setState(() => _error = context.appLocalizations.stabilityUnavailable);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Widget _group(Map<dynamic, dynamic> group) {
    final l10n = context.appLocalizations;
    final state = group['state'] as Map;
    final names = group['names'] as Map;
    final primary = state['primary'] as String? ?? '';
    final active = (state['active'] as List? ?? []).toList();
    active.sort((a, b) => a == primary ? -1 : b == primary ? 1 : 0);
    final nodes = (state['nodes'] as List? ?? []).cast<Map>();
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('${group['group']} 路 ${stabilityStatusText(context, group['status'] as String? ?? '')}', style: context.textTheme.titleMedium),
            const SizedBox(height: 8),
            Text(l10n.stabilityMeasurement),
            if ((group['pending'] as String? ?? '').isNotEmpty)
              Text('${l10n.stabilityValidating} · ${names[group['pending']]}'),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: Text(l10n.stabilityAutomatic),
              value: state['paused'] != true,
              onChanged: _busy ? null : (value) => _action(() async {
                await coreController.setStabilityMode({'group': group['group'], 'paused': !value, 'powerSave': state['powerSave'] == true});
              }),
            ),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: Text(l10n.stabilityPowerSave),
              value: state['powerSave'] == true,
              onChanged: _busy ? null : (value) => _action(() async {
                await coreController.setStabilityMode({'group': group['group'], 'paused': state['paused'] == true, 'powerSave': value});
              }),
            ),
            for (final id in active.take(3))
              Builder(builder: (context) {
                final node = nodes.firstWhere((n) => n['id'] == id);
                final at = DateTime.tryParse(node['lastSuccess'] as String? ?? '');
                final age = at == null ? 0 : DateTime.now().difference(at).inSeconds;
                final delay = (node['ewmaMs'] as num? ?? 0).round();
                return ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(id == primary ? Icons.radio_button_checked : Icons.check_circle_outline, color: context.colorScheme.primary),
                  title: Text('${id == primary ? l10n.stabilityPrimary : l10n.stabilityBackup} 路 ${names[id] ?? id}'),
                  subtitle: Text('$delay ms 路 ${l10n.stabilityAge(age)} 路 ${l10n.stabilityVerified}'),
                );
              }),
            if (active.isEmpty) Text(l10n.stabilityWaiting),
            const SizedBox(height: 12),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                OutlinedButton(
                  onPressed: _busy ? null : () => _action(() async {
                    await coreController.markStabilityFault(group['group'] as String);
                    await coreController.setStabilityMode({'group': group['group'], 'paused': state['paused'] == true, 'powerSave': state['powerSave'] == true, 'debug': true});
                  }),
                  child: Text(l10n.stabilityMark),
                ),
                FilledButton(onPressed: _busy ? null : () => _action(exportStabilityDiagnostics), child: Text(l10n.stabilityExport)),
              ],
            ),
            if ((group['debugRemainingSec'] as num? ?? 0) > 0)
              Padding(padding: const EdgeInsets.only(top: 8), child: Text(l10n.stabilityDebug((group['debugRemainingSec'] as num).round()))),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.appLocalizations;
    return CommonScaffold(
      title: l10n.stabilityTitle,
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          if (_error != null) Text(_error!),
          if (_groups.isEmpty && _error == null) Text(l10n.stabilityNoGroup),
          for (final group in _groups) _group(group as Map),
        ],
      ),
    );
  }
}

class StabilityGroupDelay extends StatefulWidget {
  const StabilityGroupDelay({super.key, required this.group, required this.child});
  final String group;
  final Widget child;
  @override
  State<StabilityGroupDelay> createState() => _StabilityGroupDelayState();
}

class _StabilityGroupDelayState extends State<StabilityGroupDelay> {
  Timer? _timer;
  String? _status;
  bool _reading = false;

  @override
  void initState() {
    super.initState();
    unawaited(_refresh());
    _timer = Timer.periodic(const Duration(seconds: 3), (_) => unawaited(_refresh()));
  }

  Future<void> _refresh() async {
    if (_reading) return;
    _reading = true;
    try {
      final groups = await coreController.getStabilityState();
      for (final group in groups.cast<Map>()) {
        if (group['group'] == widget.group && mounted) {
          setState(() => _status = group['status'] as String?);
        }
      }
    } catch (_) {
      if (mounted) setState(() => _status = 'stopped');
    } finally {
      _reading = false;
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => _status == 'ready'
      ? widget.child
      : Text(stabilityStatusText(context, _status ?? 'validating'), style: context.textTheme.labelSmall);
}
