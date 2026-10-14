package com.follow.clash.service.modules

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class StabilityNetworkObserverTest {
    @Test
    fun `available internet permits probes without captive portal validation`() {
        val observer = StabilityNetworkObserver()
        assertEquals(StabilityNetworkSnapshot(101, true), observer.update(101, true, true, true))
    }

    @Test
    fun `capability callbacks on the same available network do not reset candidates`() {
        val observer = StabilityNetworkObserver()
        observer.update(101, true, true, true)
        repeat(4) { assertNull(observer.update(101, true, true, true)) }
    }

    @Test
    fun `network identity change invalidates prior candidates`() {
        val observer = StabilityNetworkObserver()
        observer.update(101, true, true, true)
        assertEquals(StabilityNetworkSnapshot(102, true), observer.update(102, true, true, true))
    }

    @Test
    fun `lost network stops probing once`() {
        val observer = StabilityNetworkObserver()
        observer.update(101, true, true, true)
        assertEquals(StabilityNetworkSnapshot(0, false), observer.update(null, false, false, false))
        assertNull(observer.update(null, false, false, false))
    }

    @Test
    fun `suspended network stops and resumption restarts probing`() {
        val observer = StabilityNetworkObserver()
        observer.update(101, true, true, true)
        assertEquals(StabilityNetworkSnapshot(101, false), observer.update(101, true, true, false))
        assertEquals(StabilityNetworkSnapshot(101, true), observer.update(101, true, true, true))
    }

    @Test
    fun `missing internet capability and VPN cannot qualify as underlying internet`() {
        assertEquals(StabilityNetworkSnapshot(101, false), StabilityNetworkObserver().update(101, false, true, true))
        assertEquals(StabilityNetworkSnapshot(101, false), StabilityNetworkObserver().update(101, true, false, true))
    }
}
