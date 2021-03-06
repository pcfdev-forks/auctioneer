package main_test

import (
	"net/http/httptest"
	"os"
	"time"

	"code.cloudfoundry.org/auction/simulation/simulationrep"
	"code.cloudfoundry.org/bbs"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/locket"
	"code.cloudfoundry.org/rep"

	"code.cloudfoundry.org/lager"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	executorfakes "code.cloudfoundry.org/executor/fakes"
	"code.cloudfoundry.org/rep/evacuation/evacuation_context/fake_evacuation_context"
	rephandlers "code.cloudfoundry.org/rep/handlers"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/rata"
)

type FakeCell struct {
	cellID      string
	stack       string
	server      *httptest.Server
	heartbeater ifrit.Process
	logger      lager.Logger

	SimulationRep rep.SimClient
}

func SpinUpFakeCell(serviceClient bbs.ServiceClient, cellID string, stack string) *FakeCell {
	fakeRep := &FakeCell{
		cellID: cellID,
		stack:  stack,
		logger: lager.NewLogger("fake-cell"),
	}

	fakeRep.SpinUp(serviceClient)
	Eventually(func() bool {
		cells, err := serviceClient.Cells(logger)
		Expect(err).NotTo(HaveOccurred())
		return cells.HasCellID(cellID)
	}).Should(BeTrue())

	return fakeRep
}

func (f *FakeCell) LRPs() ([]rep.LRP, error) {
	state, err := f.SimulationRep.State(logger)
	if err != nil {
		return nil, err
	}
	return state.LRPs, nil
}

func (f *FakeCell) Tasks() ([]rep.Task, error) {
	state, err := f.SimulationRep.State(logger)
	if err != nil {
		return nil, err
	}
	return state.Tasks, nil
}

func (f *FakeCell) SpinUp(serviceClient bbs.ServiceClient) {
	//make a test-friendly AuctionRepDelegate using the auction package's SimulationRepDelegate
	f.SimulationRep = simulationrep.New(f.stack, "Z0", rep.Resources{
		DiskMB:     100,
		MemoryMB:   100,
		Containers: 100,
	}, []string{"my-driver"})

	//spin up an http auction server
	logger := lager.NewLogger(f.cellID)
	logger.RegisterSink(lager.NewWriterSink(GinkgoWriter, lager.INFO))

	fakeExecutorClient := new(executorfakes.FakeClient)
	fakeEvacuatable := new(fake_evacuation_context.FakeEvacuatable)

	handlers := rephandlers.New(f.SimulationRep, fakeExecutorClient, fakeEvacuatable, logger)
	router, err := rata.NewRouter(rep.Routes, handlers)
	Expect(err).NotTo(HaveOccurred())
	f.server = httptest.NewServer(router)

	presence := models.NewCellPresence(
		f.cellID,
		f.server.URL,
		"az1",
		models.NewCellCapacity(512, 1024, 124),
		[]string{},
		[]string{},
		[]string{},
		[]string{},
	)

	f.heartbeater = ifrit.Invoke(serviceClient.NewCellPresenceRunner(logger, &presence, time.Second, locket.LockTTL))
}

func (f *FakeCell) Stop() {
	f.server.Close()
	f.heartbeater.Signal(os.Interrupt)
	Eventually(f.heartbeater.Wait()).Should(Receive())
}
