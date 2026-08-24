//go:build android

package gui

/*
#cgo LDFLAGS: -lcamera2ndk -lmediandk -landroid -llog

#include <stdlib.h>
#include <string.h>
#include <jni.h>
#include <camera/NdkCameraManager.h>
#include <camera/NdkCameraDevice.h>
#include <camera/NdkCameraCaptureSession.h>
#include <media/NdkImageReader.h>

// The camera through Android's C interface rather than its Java one.
//
// Camera2 has an NDK binding, so a Go program can open a camera without a line of Java in the
// build: the callbacks below are C function pointers, which is exactly what cgo can provide, where
// the Java API would want classes implementing interfaces.

static ACameraManager *manager;
static ACameraDevice *device;
static ACameraCaptureSession *session;
static ACaptureSessionOutputContainer *outputs;
static ACaptureSessionOutput *output;
static ACameraOutputTarget *target;
static ACaptureRequest *request;
static AImageReader *reader;
static ANativeWindow *surface;

static void onDisconnected(void *ctx, ACameraDevice *d) {}
static void onError(void *ctx, ACameraDevice *d, int err) {}
static void onSessionReady(void *ctx, ACameraCaptureSession *s) {}
static void onSessionActive(void *ctx, ACameraCaptureSession *s) {}
static void onSessionClosed(void *ctx, ACameraCaptureSession *s) {}

// backCamera copies the id of the rear camera into out, falling back to the first one there is.
static int backCamera(char *out, int cap) {
	ACameraIdList *ids = NULL;
	if (ACameraManager_getCameraIdList(manager, &ids) != ACAMERA_OK || ids == NULL) {
		return 0;
	}
	if (ids->numCameras < 1) {
		ACameraManager_deleteCameraIdList(ids);
		return 0;
	}

	int found = 0;
	for (int i = 0; i < ids->numCameras && !found; i++) {
		ACameraMetadata *about = NULL;
		if (ACameraManager_getCameraCharacteristics(manager, ids->cameraIds[i], &about) != ACAMERA_OK) {
			continue;
		}

		ACameraMetadata_const_entry facing;
		if (ACameraMetadata_getConstEntry(about, ACAMERA_LENS_FACING, &facing) == ACAMERA_OK &&
			facing.count > 0 && facing.data.u8[0] == ACAMERA_LENS_FACING_BACK) {
			strncpy(out, ids->cameraIds[i], cap - 1);
			found = 1;
		}
		ACameraMetadata_free(about);
	}
	if (!found) {
		strncpy(out, ids->cameraIds[0], cap - 1);
		found = 1;
	}

	ACameraManager_deleteCameraIdList(ids);
	return found;
}

static int dropCameraStart(int width, int height) {
	if (manager != NULL) {
		return 1; // already running
	}

	manager = ACameraManager_create();
	if (manager == NULL) {
		return 0;
	}

	char id[64];
	memset(id, 0, sizeof id);
	if (!backCamera(id, sizeof id)) {
		return 0;
	}

	if (AImageReader_new(width, height, AIMAGE_FORMAT_YUV_420_888, 4, &reader) != AMEDIA_OK) {
		return 0;
	}
	if (AImageReader_getWindow(reader, &surface) != AMEDIA_OK) {
		return 0;
	}
	ANativeWindow_acquire(surface);

	ACameraDevice_StateCallbacks about = {
		.context = NULL,
		.onDisconnected = onDisconnected,
		.onError = onError,
	};
	if (ACameraManager_openCamera(manager, id, &about, &device) != ACAMERA_OK) {
		return 0;
	}

	if (ACaptureSessionOutputContainer_create(&outputs) != ACAMERA_OK) {
		return 0;
	}
	if (ACaptureSessionOutput_create(surface, &output) != ACAMERA_OK) {
		return 0;
	}
	ACaptureSessionOutputContainer_add(outputs, output);

	ACameraCaptureSession_stateCallbacks state = {
		.context = NULL,
		.onClosed = onSessionClosed,
		.onReady = onSessionReady,
		.onActive = onSessionActive,
	};
	if (ACameraDevice_createCaptureSession(device, outputs, &state, &session) != ACAMERA_OK) {
		return 0;
	}

	if (ACameraDevice_createCaptureRequest(device, TEMPLATE_PREVIEW, &request) != ACAMERA_OK) {
		return 0;
	}
	if (ACameraOutputTarget_create(surface, &target) != ACAMERA_OK) {
		return 0;
	}
	ACaptureRequest_addTarget(request, target);

	if (ACameraCaptureSession_setRepeatingRequest(session, NULL, 1, &request, NULL) != ACAMERA_OK) {
		return 0;
	}
	return 1;
}

// dropCameraFrame copies the newest frame's brightness into dst, one byte a pixel.
//
// Only the Y plane: a QR code is black and white, and the colour planes are two thirds of the data
// for none of the information.
static int dropCameraFrame(unsigned char *dst, int cap, int *width, int *height) {
	if (reader == NULL) {
		return 0;
	}

	AImage *frame = NULL;
	if (AImageReader_acquireLatestImage(reader, &frame) != AMEDIA_OK || frame == NULL) {
		return 0;
	}

	int32_t w = 0, h = 0, stride = 0, length = 0;
	unsigned char *plane = NULL;

	AImage_getWidth(frame, &w);
	AImage_getHeight(frame, &h);
	AImage_getPlaneRowStride(frame, 0, &stride);
	AImage_getPlaneData(frame, 0, &plane, &length);

	if (plane == NULL || w <= 0 || h <= 0 || w*h > cap) {
		AImage_delete(frame);
		return 0;
	}

	for (int y = 0; y < h; y++) {
		memcpy(dst + y*w, plane + y*stride, w);
	}
	*width = w;
	*height = h;

	AImage_delete(frame);
	return 1;
}

static void dropCameraStop(void) {
	if (session != NULL) {
		ACameraCaptureSession_stopRepeating(session);
		ACameraCaptureSession_close(session);
		session = NULL;
	}
	if (request != NULL) {
		ACaptureRequest_free(request);
		request = NULL;
	}
	if (target != NULL) {
		ACameraOutputTarget_free(target);
		target = NULL;
	}
	if (output != NULL) {
		ACaptureSessionOutput_free(output);
		output = NULL;
	}
	if (outputs != NULL) {
		ACaptureSessionOutputContainer_free(outputs);
		outputs = NULL;
	}
	if (device != NULL) {
		ACameraDevice_close(device);
		device = NULL;
	}
	if (surface != NULL) {
		ANativeWindow_release(surface);
		surface = NULL;
	}
	if (reader != NULL) {
		AImageReader_delete(reader);
		reader = NULL;
	}
	if (manager != NULL) {
		ACameraManager_delete(manager);
		manager = NULL;
	}
}

// dropCameraAllowed asks Android whether this app may use the camera, and asks the person if it
// has not been decided. Returns 1 when it is already granted.
//
// The permission belongs to an Activity, which is why the view has to come from Gio: a process
// cannot put a dialog on the screen, a screen can.
static int dropCameraAllowed(uintptr_t vmp, uintptr_t viewp, int ask) {
	JavaVM *vm = (JavaVM *)vmp;
	jobject view = (jobject)viewp;
	if (vm == NULL || view == NULL) {
		return 0;
	}

	JNIEnv *env = NULL;
	int attached = 0;
	if ((*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6) != JNI_OK) {
		if ((*vm)->AttachCurrentThread(vm, &env, NULL) != JNI_OK) {
			return 0;
		}
		attached = 1;
	}

	int granted = 0;

	jclass viewClass = (*env)->GetObjectClass(env, view);
	jmethodID getContext = (*env)->GetMethodID(env, viewClass, "getContext", "()Landroid/content/Context;");
	jobject context = (*env)->CallObjectMethod(env, view, getContext);

	if (context != NULL) {
		jclass contextClass = (*env)->GetObjectClass(env, context);
		jmethodID check = (*env)->GetMethodID(env, contextClass, "checkSelfPermission", "(Ljava/lang/String;)I");
		jstring want = (*env)->NewStringUTF(env, "android.permission.CAMERA");

		if (check != NULL) {
			granted = (*env)->CallIntMethod(env, context, check, want) == 0;
		}

		if (!granted && ask) {
			jmethodID request = (*env)->GetMethodID(env, contextClass, "requestPermissions", "([Ljava/lang/String;I)V");
			if (request != NULL) {
				jclass stringClass = (*env)->FindClass(env, "java/lang/String");
				jobjectArray wanted = (*env)->NewObjectArray(env, 1, stringClass, want);
				(*env)->CallVoidMethod(env, context, request, wanted, 1);
			}
			(*env)->ExceptionClear(env);
		}
	}

	if (attached) {
		(*vm)->DetachCurrentThread(vm);
	}
	return granted;
}
*/
import "C"

import (
	"errors"
	"image"
	"sync"
	"time"
	"unsafe"

	"gioui.org/app"
)

const (
	frameWidth  = 640
	frameHeight = 480
)

// The view Gio last handed over, which is what a permission dialog needs.
var seen struct {
	mu sync.Mutex
	at uintptr
}

func noteView(e app.ViewEvent) {
	view, ok := e.(app.AndroidViewEvent)
	if !ok {
		return
	}

	seen.mu.Lock()
	seen.at = view.View
	seen.mu.Unlock()
}

func viewNow() uintptr {
	seen.mu.Lock()
	defer seen.mu.Unlock()

	return seen.at
}

// allowed reports whether the camera may be opened, asking the person the first time.
//
// Android answers a permission request on its own thread and there is nothing to wait on, so this
// asks and then watches for the answer rather than blocking on one.
func allowed(within time.Duration) bool {
	view := viewNow()
	if view == 0 {
		return false
	}
	vm := app.JavaVM()

	if C.dropCameraAllowed(C.uintptr_t(vm), C.uintptr_t(view), 0) == 1 {
		return true
	}
	C.dropCameraAllowed(C.uintptr_t(vm), C.uintptr_t(view), 1)

	until := time.Now().Add(within)
	for time.Now().Before(until) {
		time.Sleep(200 * time.Millisecond)
		if C.dropCameraAllowed(C.uintptr_t(vm), C.uintptr_t(view), 0) == 1 {
			return true
		}
	}
	return false
}

// openCamera starts the rear camera feeding an image reader.
func openCamera() error {
	if C.dropCameraStart(C.int(frameWidth), C.int(frameHeight)) != 1 {
		C.dropCameraStop()
		return errors.New("the camera could not be opened")
	}
	return nil
}

func closeCamera() { C.dropCameraStop() }

// nextFrame returns the newest frame, or nil when none has arrived since the last one.
func nextFrame(into []byte) *image.Gray {
	var w, h C.int

	if C.dropCameraFrame((*C.uchar)(unsafe.Pointer(&into[0])), C.int(len(into)), &w, &h) != 1 {
		return nil
	}

	width, height := int(w), int(h)
	frame := &image.Gray{
		Pix:    into[:width*height],
		Stride: width,
		Rect:   image.Rect(0, 0, width, height),
	}
	return frame
}
