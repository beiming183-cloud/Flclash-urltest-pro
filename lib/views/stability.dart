import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/core/core.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/icons/icons.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:material_ui/material_ui.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class StabilityPanel extends ConsumerStatefulWidget {
  const StabilityPanel({super.key});

  @override
  ConsumerState<StabilityPanel> createState() => _StabilityPanelState();
}

class _StabilityPanelState extends ConsumerState<StabilityPanel>
    with WidgetsBindingObserver {
  Timer? _timer;
  List<dynamic> _groups = [];
  bool _reading = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _startRefresh();
  }

  void _startRefresh() {
    _timer?.cancel();
    unawaited(_refresh());
    _timer = Timer.periodic(const Duration(seconds: 5), (_) {
      unawaited(_refresh());
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _startRefresh();
    } else {
      _timer?.cancel();
    }
  }

  Future<void> _refresh() async {
    if (_reading ||
        !mounted ||
        ref.read(coreStatusProvider) != CoreStatus.connected) {
      return;
    }
    _reading = true;
    try {
      final groups = await coreController.getStabilityState();
      if (mounted) setState(() => _groups = groups);
    } catch (_) {
      if (mounted) {
        setState(() => _error = context.appLocalizations.stabilityUnavailable);
      }
    } finally {
      _reading = false;
    }
  }

  Future<void> _run(Future<void> Function() action) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await action();
      await _refresh();
    } catch (_) {
      if (mounted) {
        setState(() => _error = context.appLocalizations.stabilityActionFailed);
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _mode(
    Map<String, dynamic> group, {
    bool? paused,
    bool? powerSave,
    bool? debug,
  }) async {
    final state = Map<String, dynamic>.from(group['state'] as Map);
    final ok = await coreController.setStabilityMode({
      'group': group['group'],
      'paused': paused ?? state['paused'] == true,
      'powerSave': powerSave ?? state['powerSave'] == true,
      'debug': debug ?? (group['debugRemainingSec'] as num) > 0,
    });
    if (!ok) throw StateError('stability mode not applied');
  }

  Future<void> _export() async {
    final data = await coreController.exportStabilityDiagnostics();
    if (data == null) throw StateError('diagnostic export unavailable');
    final bytes = base64Decode(data['base64'] as String);
    if (bytes.length > 2 * 1024 * 1024 || bytes.length != data['size']) {
      throw StateError('invalid diagnostic archive');
    }
    final path = '${await appPath.tempPath}/stability-diagnostic.zip';
    final file = File(path);
    try {
      await file.writeAsBytes(bytes, flush: true);
      await picker.saveFileWithPath('FlClash-stability-diagnostic.zip', path);
    } finally {
      if (await file.exists()) await file.delete();
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l = context.appLocalizations;
    return Card(
      margin: EdgeInsets.zero,
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const GlyphIcon(AppGlyphs.networkCheck),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    l.stabilityTitle,
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                ),
                IconButton(
                  tooltip: l.stabilityRefresh,
                  onPressed: _busy ? null : _refresh,
                  icon: const GlyphIcon(AppGlyphs.refresh),
                ),
              ],
            ),
            if (_groups.isEmpty) Text(l.stabilityImportTip),
            for (final raw in _groups)
              _group(Map<String, dynamic>.from(raw as Map)),
            if (_busy) const LinearProgressIndicator(),
            if (_error != null)
              Text(
                _error!,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
          ],
        ),
      ),
    );
  }

  Widget _group(Map<String, dynamic> group) {
    final l = context.appLocalizations;
    final state = Map<String, dynamic>.from(group['state'] as Map);
    final names = Map<String, dynamic>.from(group['names'] as Map);
    final primary = state['primary'] as String;
    final active = (state['active'] as List).cast<String>();
    final nodes = (state['nodes'] as List)
        .map((node) => Map<String, dynamic>.from(node as Map))
        .toList();
    final debug = (group['debugRemainingSec'] as num).toInt();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SizedBox(height: 8),
        Text(
          group['group'] as String,
          style: Theme.of(context).textTheme.titleSmall,
        ),
        Text(
          primary.isEmpty
              ? l.stabilityNoChain
              : '${l.stabilityPrimary}: ${names[primary]}',
        ),
        Text(
          '${l.stabilityReason}: ${state['lastSwitchReason']} · ${state['budgetUsed']}/${state['budgetLimit']}',
        ),
        SwitchListTile(
          contentPadding: EdgeInsets.zero,
          title: Text(l.stabilityAutomatic),
          value: state['paused'] != true,
          onChanged: _busy
              ? null
              : (value) => _run(() => _mode(group, paused: !value)),
        ),
        ExpansionTile(
          tilePadding: EdgeInsets.zero,
          title: Text('${l.stabilityCandidates} (${active.length}/3)'),
          children: [
            for (final node in nodes.where(
              (node) => active.contains(node['id']),
            ))
              ListTile(
                contentPadding: EdgeInsets.zero,
                title: Text(names[node['id']] as String),
                subtitle: Text(
                  '${l.stabilityLatency}: ${(node['ewmaMs'] as num).round()} ms · P95 ${(node['p95Ms'] as num).round()} ms\n${l.stabilityLastCheck}: ${_age(node['lastCheck'] as String)} · ${l.stabilitySamples}: ${node['sampleCount']}',
                ),
                trailing: TextButton(
                  onPressed: _busy
                      ? null
                      : () => _run(() async {
                          final result = await coreController
                              .probeStabilityCandidate({
                                'group': group['group'],
                                'node': node['id'],
                              });
                          if (result == null || result['OK'] != true) {
                            if (mounted) {
                              setState(
                                () => _error =
                                    '${l.stabilityActionFailed}: ${result?['Error'] ?? ''}',
                              );
                            }
                          }
                        }),
                  child: Text(l.stabilityTest),
                ),
              ),
            SwitchListTile(
              title: Text(l.stabilityPowerSave),
              value: state['powerSave'] == true,
              onChanged: _busy
                  ? null
                  : (value) => _run(() => _mode(group, powerSave: value)),
            ),
            SwitchListTile(
              title: Text(l.stabilityDebug),
              subtitle: Text(debug > 0 ? '$debug s' : l.stabilityDebugTip),
              value: debug > 0,
              onChanged: _busy
                  ? null
                  : (value) => _run(() => _mode(group, debug: value)),
            ),
            Wrap(
              spacing: 8,
              children: [
                OutlinedButton(
                  onPressed: _busy
                      ? null
                      : () => _run(() async {
                          if (!await coreController.markStabilityFault(
                            group['group'] as String,
                          )) {
                            throw StateError('fault mark failed');
                          }
                        }),
                  child: Text(l.stabilityMark),
                ),
                OutlinedButton(
                  onPressed: _busy ? null : () => _run(_export),
                  child: Text(l.stabilityExport),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(l.stabilityReconnectTip),
          ],
        ),
      ],
    );
  }

  String _age(String value) {
    final date = DateTime.tryParse(value);
    if (date == null || date.year < 2000) return '—';
    final seconds = DateTime.now().difference(date).inSeconds;
    return '${seconds < 0 ? 0 : seconds} s';
  }
}
