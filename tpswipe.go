package main

import (
	"gopkg.in/gcfg.v1"
	"flag"
	"fmt"
	"github.com/BurntSushi/xgbutil"
	"github.com/BurntSushi/xgbutil/ewmh"
	"github.com/BurntSushi/xgbutil/icccm"
	"github.com/gvalkov/golang-evdev"
	"github.com/mattn/go-shellwords"
	"math"
	"os"
	"os/exec"
	"os/user"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type gestureType int

const (
	UNKNOWN gestureType = iota
	SWIPE_UP
	SWIPE_DOWN
	SWIPE_LEFT
	SWIPE_RIGHT
	PINCH
	SPREAD
)

const (
	DIST_SWIPE  = 1100 // The distance the finger must move to trigger a swipe (unknown unit)
	DIST_OTHER  = 500  // The distance the finger must move to trigger other gestures (unknown unit)
	CHECK_DELAY = 50   // Delay between checking for gestures (ms)
)

type Gesture struct {
	GestureType gestureType
	FingerCount int
}

func (gest Gesture) String() string {

	return fmt.Sprintf("%s(%d)", getGestureTypeName(gest.GestureType), gest.FingerCount)

}

/*
	Finger struct
*/

type Finger struct {
	// The first x and y postion that is reported after the finger touches the pad
	// or after the finger has been reset
	FirstX int
	FirstY int
	// The last x and y postion that is reported while the fingers is touching the pad
	LastX int
	LastY int
	// If the position has been set after the first touch or reset
	HasPositionX bool
	HasPositionY bool
	// If this finger is currently touching the pas
	IsActive bool
	// The time since the first touch or reset
	ActivationTime time.Time
}

func (finger *Finger) activate() {
	finger.IsActive = true
	finger.reset()
}

func (finger *Finger) deactivate() {
	finger.IsActive = false
}

func (finger *Finger) reset() {

	finger.ActivationTime = time.Now()
	finger.HasPositionX = false
	finger.HasPositionY = false
}

// Set the current x postion of the finger
func (finger *Finger) setPositionX(x int) {

	if !finger.HasPositionX {
		finger.FirstX = x
		finger.HasPositionX = true
	}

	finger.LastX = x

}

// Set the current y postion of the finger
func (finger *Finger) setPositionY(y int) {

	if !finger.HasPositionY {
		finger.FirstY = y
		finger.HasPositionY = true
	}

	finger.LastY = y

}

// If the position of the finger has been set after the first
// touch or since the last reset
func (finger *Finger) hasPosition() bool {
	return finger.HasPositionX && finger.HasPositionY
}

// Return the angle (0-360 degrees) of the last movement of the finger since
// relative to the x-axis since the
func (finger *Finger) getAngle() int {

	if !finger.hasPosition() {
		return 0
	}

	dx := finger.LastX - finger.FirstX
	dy := -(finger.LastY - finger.FirstY)

	angle := int(math.Atan2(float64(dy), float64(dx)) * 180.0 / math.Pi)

	if angle < 0 {
		return 360 + angle
	}
	return angle

}

// Returns the direction of the movement of finger relative to the
// first position since the first touch or reset.
func (finger *Finger) getDirection() gestureType {

	angle := finger.getAngle()

	switch {

	case angle >= 45 && angle <= 135:
		return SWIPE_UP
	case angle >= 135 && angle <= 225:
		return SWIPE_LEFT
	case angle >= 225 && angle <= 315:
		return SWIPE_DOWN
	case angle >= 315 || angle <= 45:
		return SWIPE_RIGHT
	default:
		return UNKNOWN

	}

}

// The distance the finger has moved relative to the
// first postion of the finger since the first touch or reset
func (finger *Finger) getDistance() int {

	if !finger.hasPosition() {
		return 0
	}

	return calculateDistance(finger.LastX, finger.LastY, finger.FirstX, finger.FirstY)
}

// Check if the finger has has a movement that is considered a swipeing motion.
func (finger *Finger) hasSwiped(distanceThreshold int) bool {

	if !finger.hasPosition() {
		return false
	}

	if diff := time.Since(finger.ActivationTime); diff < (CHECK_DELAY * time.Millisecond) {
		return false
	}

	return finger.getDistance() > distanceThreshold

}

/*
	EventHandler ========================
*/
type EventHandler struct {
	// Five fingers
	fingers [5]Finger
	// The current finger that is modified by the events
	currentSlot int
	// The last reported gesture type
	lastGesture gestureType
	// The number of active fingers touching the pad
	fingerCount int
	// Used to keep track of the last check for a gesture
	checkTimer time.Time
	// Suspend any new gestures until the fingers are lifted
	// from the pad. Used to prevent reporting of multiple gestures in
	// one movement.
	suspend bool
	// Channel for detected gestures
	Gestures chan Gesture

	// The number of fingerCounts that have gestures
	// defined in the config. There is no need to detect
	// gestures if the no action is defined for that number
	// of fingers tounching.
	configuredFingers map[int]bool
}

func (handler *EventHandler) resetFingers() {

	for i := range handler.fingers {
		handler.fingers[i].reset()
	}
	handler.checkTimer = time.Now()

}

func (handler *EventHandler) handleEvent(event *evdev.InputEvent) {

	switch event.Type {

	case evdev.EV_SYN:
		handler.handleSynEvent(event)
	case evdev.EV_ABS:
		handler.handleAbsEvent(event)
	}

}

func (handler *EventHandler) handleSynEvent(event *evdev.InputEvent) {

	// Only check for gestures after a report event and not suspended
	if event.Code == evdev.SYN_REPORT && !handler.suspend {
		handler.detectGesture()
	}

}

func (handler *EventHandler) detectGesture() {

	// Time since last reset
	timeDiff := time.Since(handler.checkTimer)

	// Do we have enought finger or enought time passed since the last reset
	if _, ok := handler.configuredFingers[handler.fingerCount]; !ok || handler.fingerCount < 2 || timeDiff < (CHECK_DELAY*time.Millisecond) {
		// fmt.Println(handler.fingerCount, timeDiff)
		return
	}

	isSwipe := false
	gesture := UNKNOWN

	// We only register a swipe if all the fingers
	// reports a swipe in the same direction
	for i := range handler.fingers {

		fing := &handler.fingers[i]

		if !fing.IsActive {
			continue
		}

		if fing.hasSwiped(DIST_SWIPE) {
			isSwipe = true
			if i == 0 {
				gesture = fing.getDirection()
			} else if fing.getDirection() != gesture {
				// Not all fingers moved in the same direction
				// so its not a swipe
				isSwipe = false
				break
			}
		} else {

			// Not all fingers has moved enough to register a swipe
			// so we return

			// If it has been more than some ms since the last reset and
			// no gesture is detected we reset
			if timeDiff > (220 * time.Millisecond) {
				handler.resetFingers()
			}
			return
		}
	}

	if isSwipe {

		if handler.lastGesture != gesture {

			handler.emitGesture(Gesture{gesture, handler.fingerCount})
			// Suspend gestures until fingers are lifted from the pad
			handler.suspend = true
		}

	} else {

		// It was not a straight swipe so check for other gestures
		gesture := handler.calculateGesture()

		if gesture != UNKNOWN {

			if handler.lastGesture != gesture {

				handler.emitGesture(Gesture{gesture, handler.fingerCount})
				// Suspend gestures until fingers are lifted from the pad
				handler.suspend = true

			}

		}
	}

	// When we get here we have either reported a swipe and is suspended or it was an unknown movement
	// so we reset the fingers to check for a new gesture if the last movement was not a gesture
	handler.resetFingers()

}

func (handler *EventHandler) emitGesture(gesture Gesture) {

	handler.lastGesture = gesture.GestureType
	handler.Gestures <- gesture

}

func (handler *EventHandler) handleAbsEvent(event *evdev.InputEvent) {

	switch event.Code {

	case evdev.ABS_MT_SLOT:
		handler.currentSlot = int(event.Value)
	case evdev.ABS_MT_TRACKING_ID:

		prevCount := handler.fingerCount

		if event.Value == -1 {
			handler.fingerCount -= 1
			handler.fingers[handler.currentSlot].deactivate()
		} else {
			handler.fingerCount += 1
			handler.fingers[handler.currentSlot].activate()
		}

		handler.resetFingers()

		// If previously no fingers was toucing we get
		// ready to handle a new gesture
		if prevCount == 0 {
			handler.lastGesture = UNKNOWN
			handler.suspend = false
		}

	case evdev.ABS_MT_POSITION_X:
		handler.fingers[handler.currentSlot].setPositionX(int(event.Value))
	case evdev.ABS_MT_POSITION_Y:
		handler.fingers[handler.currentSlot].setPositionY(int(event.Value))
	}

}

// Returns the gesture type from the last movement of the
// fingers if it was not a stright swipe to one of the sides
func (handler *EventHandler) calculateGesture() gestureType {

	var startPositions [][2]int
	var endPositions [][2]int

	for i := range handler.fingers {

		fing := &handler.fingers[i]

		if !fing.IsActive {
			continue
		}
		if !fing.hasSwiped(DIST_OTHER) {
			return UNKNOWN
		}

		startPositions = append(startPositions, [2]int{fing.FirstX, fing.FirstY})
		endPositions = append(endPositions, [2]int{fing.LastX, fing.LastY})

	}

	// Calculate the circumference around the fingers in the start and end position
	// to determine if the fingers was pinched or spread.
	start := calculateCircumference(startPositions)
	end := calculateCircumference(endPositions)

	if start > end {
		return PINCH
	} else {
		return SPREAD
	}

}

// Listen for event from the input device
func (handler *EventHandler) run(dev *evdev.InputDevice) {

	var events []evdev.InputEvent

	for {
		events, _ = dev.Read()
		for i := range events {

			handler.handleEvent(&events[i])

		}

	}

}

/*
	Helper functions =====================
*/

// Calculate the circumference around the points
func calculateCircumference(points [][2]int) int {

	if len(points) == 2 {
		return calculateDistance(points[0][0], points[0][1], points[1][0], points[1][1])
	}

	total := 0
	p0 := points[0]

	for _, p := range points {
		total += calculateDistance(p0[0], p0[1], p[0], p[1])
		p0 = p
	}

	total += calculateDistance(points[0][0], points[0][1], p0[0], p0[1])

	return total

}

// Calculate distance between two points
func calculateDistance(x0, y0, x, y int) int {

	return int(math.Sqrt(math.Pow(float64(x-x0), 2) +
		math.Pow(float64(y-y0), 2)))

}

// Get name of the gesture type
func getGestureTypeName(gesture gestureType) string {
	switch gesture {
	case SWIPE_UP:
		return "Swipe Up"
	case SWIPE_DOWN:
		return "Swipe Down"
	case SWIPE_LEFT:
		return "Swipe Left"
	case SWIPE_RIGHT:
		return "Swipe Right"
	case PINCH:
		return "Pinch"
	case SPREAD:
		return "Spread"
	default:
		return "UNKNOWN"

	}
}

// Create a command from a string
func createCommand(command string) *exec.Cmd {

	args, err := shellwords.Parse(command)

	if err != nil {
		fmt.Println(err)
		return nil
	}

	if len(args) > 1 {
		return exec.Command(args[0], (args[1:])...)

	}
	return exec.Command(args[0])

}

func getActiveWindowClass(xutil *xgbutil.XUtil) (string, error) {

	client, err := ewmh.ActiveWindowGet(xutil)

	if err != nil {
		return "", err
	}

	class, err := icccm.WmClassGet(xutil, client)

	if err != nil {
		return "", err
	}

	return class.Class, nil

}

func getCommand(gest *Gesture, actions *ActionCollection) *exec.Cmd {

	var cmd *exec.Cmd

	switch gest.GestureType {
	case SWIPE_UP:
		switch {
		case gest.FingerCount == 2 && len(actions.Swipe2Up) > 0:
			cmd = createCommand(actions.Swipe2Up)
		case gest.FingerCount == 3 && len(actions.Swipe3Up) > 0:
			cmd = createCommand(actions.Swipe3Up)
		case gest.FingerCount == 4 && len(actions.Swipe4Up) > 0:
			cmd = createCommand(actions.Swipe4Up)
		case gest.FingerCount == 5 && len(actions.Swipe5Up) > 0:
			cmd = createCommand(actions.Swipe5Up)
		}
	case SWIPE_DOWN:
		switch {
		case gest.FingerCount == 2 && len(actions.Swipe2Down) > 0:
			cmd = createCommand(actions.Swipe2Down)
		case gest.FingerCount == 3 && len(actions.Swipe3Down) > 0:
			cmd = createCommand(actions.Swipe3Down)
		case gest.FingerCount == 4 && len(actions.Swipe4Down) > 0:
			cmd = createCommand(actions.Swipe4Down)
		case gest.FingerCount == 5 && len(actions.Swipe5Down) > 0:
			cmd = createCommand(actions.Swipe5Down)
		}
	case SWIPE_LEFT:
		switch {
		case gest.FingerCount == 2 && len(actions.Swipe2Left) > 0:
			cmd = createCommand(actions.Swipe2Left)
		case gest.FingerCount == 3 && len(actions.Swipe3Left) > 0:
			cmd = createCommand(actions.Swipe3Left)
		case gest.FingerCount == 4 && len(actions.Swipe4Left) > 0:
			cmd = createCommand(actions.Swipe4Left)
		case gest.FingerCount == 5 && len(actions.Swipe5Left) > 0:
			cmd = createCommand(actions.Swipe5Left)
		}
	case SWIPE_RIGHT:
		switch {
		case gest.FingerCount == 2 && len(actions.Swipe2Right) > 0:
			cmd = createCommand(actions.Swipe2Right)
		case gest.FingerCount == 3 && len(actions.Swipe3Right) > 0:
			cmd = createCommand(actions.Swipe3Right)
		case gest.FingerCount == 4 && len(actions.Swipe4Right) > 0:
			cmd = createCommand(actions.Swipe4Right)
		case gest.FingerCount == 5 && len(actions.Swipe5Right) > 0:
			cmd = createCommand(actions.Swipe5Right)
		}
	case PINCH:
		switch {
		case gest.FingerCount == 2 && len(actions.Pinch2) > 0:
			cmd = createCommand(actions.Pinch2)
		case gest.FingerCount == 3 && len(actions.Pinch3) > 0:
			cmd = createCommand(actions.Pinch3)
		case gest.FingerCount == 4 && len(actions.Pinch4) > 0:
			cmd = createCommand(actions.Pinch4)
		case gest.FingerCount == 5 && len(actions.Pinch5) > 0:
			cmd = createCommand(actions.Pinch5)
		}
	case SPREAD:
		switch {
		case gest.FingerCount == 2 && len(actions.Spread2) > 0:
			cmd = createCommand(actions.Spread2)
		case gest.FingerCount == 3 && len(actions.Spread3) > 0:
			cmd = createCommand(actions.Spread3)
		case gest.FingerCount == 4 && len(actions.Spread4) > 0:
			cmd = createCommand(actions.Spread4)
		case gest.FingerCount == 5 && len(actions.Spread5) > 0:
			cmd = createCommand(actions.Spread5)
		}

	}

	return cmd

}

// Do something when a gesture arrives
func handleGesture(gest *Gesture, xutil *xgbutil.XUtil, cfg *Config) {

	var cmd *exec.Cmd

	className, _ := getActiveWindowClass(xutil)

	actions := cfg.Actions[className]

	if actions == nil {
		// If its no specific window actions try the global actions
		actions = cfg.Actions[""]
	} else {
		cmd = getCommand(gest, actions)
		// If there is no action is specified for this gesture type try the global actions
		if cmd == nil {
			actions = cfg.Actions[""]
		}
	}

	// If no actions is defined just return
	if actions == nil {
		return
	}

	cmd = getCommand(gest, actions)

	if cmd != nil {
		err := cmd.Run()
		if err != nil {
			fmt.Println("Failed to run command:", err)
		}
	}

}

// Get the finger counts that have actions defined in the
// config.
func getConfiguredFingers(cfg *Config) map[int]bool {

	fingers := make(map[int]bool)

	for _, actions := range cfg.Actions {

		val := reflect.ValueOf(*actions)

		for i := 0; i < val.NumField(); i++ {

			field := val.Field(i)

			if len(field.String()) == 0 {
				continue
			}

			for j := 1; j <= 5; j++ {

				if strings.Index(val.Type().Field(i).Name, strconv.Itoa(j)) != -1 {
					fingers[j] = true
				}

			}

		}

	}

	return fingers

}

func main() {

	usr, err := user.Current()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	configFile := flag.String("config",
		fmt.Sprintf("%s/.config/tpswipe.conf", usr.HomeDir),
		"Config file")

	testTouches := flag.Bool("test", false, "Test gestures")

	flag.Parse()

	var cfg Config

	err = gcfg.ReadFileInto(&cfg, *configFile)

	if err != nil {
		fmt.Println("Config error:", err)
		os.Exit(1)
	}

	if len(cfg.Device.Path) == 0 {
		fmt.Println("No input device path in config")
		os.Exit(1)
	}

	dev, err := evdev.Open(cfg.Device.Path)

	if err != nil {
		fmt.Println("Failed to open divice:", err)
		os.Exit(1)
	}

	configuredFingers := getConfiguredFingers(&cfg)

	handler := EventHandler{Gestures: make(chan Gesture), configuredFingers: configuredFingers}

	// Listen for events
	go handler.run(dev)

	if *testTouches {
		fmt.Println("Try to do some gestures on the trackpad")
		for {
			fmt.Println("Detected:", <-handler.Gestures)
		}

	} else {

		xutil, err := xgbutil.NewConn()

		if err != nil {
			fmt.Println("Failed:", err)
			os.Exit(1)
		}

		for {

			gest := <-handler.Gestures
			go handleGesture(&gest, xutil, &cfg)

		}
	}

}

type ActionCollection struct {
	Swipe2Left string
	Swipe3Left string
	Swipe4Left string
	Swipe5Left string

	Swipe2Right string
	Swipe3Right string
	Swipe4Right string
	Swipe5Right string

	Swipe2Up string
	Swipe3Up string
	Swipe4Up string
	Swipe5Up string

	Swipe2Down string
	Swipe3Down string
	Swipe4Down string
	Swipe5Down string

	Pinch2 string
	Pinch3 string
	Pinch4 string
	Pinch5 string

	Spread2 string
	Spread3 string
	Spread4 string
	Spread5 string
}

type Config struct {
	Device struct {
		Path string
	}
	Actions map[string]*ActionCollection
}
