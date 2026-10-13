import 'package:fl_clash/core/core.dart';
import 'package:fl_clash/core/interface.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:fl_clash/state.dart';
import 'package:fl_clash/views/stability.dart';
import 'package:material_ui/material_ui.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/test_app.dart';

class _StabilityHandler extends CoreHandlerInterface {
  int probes = 0;
  int changes = 0;
  bool fail = false;
  Map<String, dynamic>? mode;

  @override
  Future<List<dynamic>> getStabilityState() async => [
    {
      'group': 'IP1',
      'debugRemainingSec': 0,
      'names': {'node-a': 'IP1 relay A'},
      'state': {
        'primary': 'node-a',
        'active': ['node-a'],
        'paused': false,
        'powerSave': false,
        'lastSwitchReason': 'promote',
        'budgetUsed': 5,
        'budgetLimit': 900,
        'nodes': [
          {
            'id': 'node-a',
            'ewmaMs': 120,
            'p95Ms': 150,
            'lastCheck': DateTime.now().toUtc().toIso8601String(),
            'sampleCount': 3,
          },
        ],
      },
    },
  ];

  @override
  Future<Map<String, dynamic>?> probeStabilityCandidate(
    Map<String, dynamic> params,
  ) async {
    probes++;
    if (fail) throw StateError('injected RPC failure');
    return {'OK': true};
  }

  @override
  Future<bool> setStabilityMode(Map<String, dynamic> params) async {
    mode = params;
    return true;
  }

  @override
  Future<T?> invokeMethod<T>({
    required CoreMethod method,
    Object? arguments,
    Duration? timeout,
  }) async {
    if (method == CoreMethod.changeProxy) changes++;
    throw UnimplementedError();
  }

  @override
  Future<CoreLifecycleResult> start() async => const CoreLifecycleResult(
    revision: 1,
    outcome: CoreLifecycleOutcome.applied,
  );
  @override
  Future<CoreLifecycleResult> restart() => start();
  @override
  Future<CoreLifecycleResult> stop() => start();
  @override
  Future<CoreLifecycleResult> close() => start();
}

void main() {
  Future<_StabilityHandler> mount(WidgetTester tester) async {
    final handler = _StabilityHandler();
    final container = ProviderContainer(
      overrides: [
        coreHandlerProvider.overrideWithValue(CoreController.scoped(handler)),
      ],
    );
    addTearDown(container.dispose);
    globalState.container = container;
    container.read(coreStatusProvider.notifier).value = CoreStatus.connected;
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const TestApp(
          child: Scaffold(body: SingleChildScrollView(child: StabilityPanel())),
        ),
      ),
    );
    await tester.pump();
    return handler;
  }

  testWidgets(
    'renders full chain state and candidate test leaves selection alone',
    (tester) async {
      final handler = await mount(tester);
      expect(find.text('Current chain: IP1 relay A'), findsOneWidget);
      await tester.tap(find.text('Active candidates (1/3)'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Test'));
      await tester.pumpAndSettle();
      expect(handler.probes, 1);
      expect(handler.changes, 0);
      expect(find.byType(LinearProgressIndicator), findsNothing);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('probe exception clears progress and keeps controls usable', (
    tester,
  ) async {
    final handler = await mount(tester);
    handler.fail = true;
    await tester.tap(find.text('Active candidates (1/3)'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Test'));
    await tester.pumpAndSettle();
    expect(
      find.text('Action failed; retry or export diagnostics'),
      findsOneWidget,
    );
    expect(find.byType(LinearProgressIndicator), findsNothing);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
  });
}
