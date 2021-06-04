package accesscontroller

import "testing"

func TestMapClientRouter(t *testing.T) {

	router := NewMapClientRouter()

	router.AddClient("node1", struct{}{})

	val, err := router.GetClient("node1")
	if err != nil {
		t.Errorf("Expected nil error, but got '%s'", err)
	} else {
		if val != struct{}{} {
			t.Errorf("Expected empty struct, but got '%v'", val)
		}
	}

	_, err = router.GetClient("missing")
	if err != ErrClientNotFound {
		t.Errorf("Expected error '%s', but got '%s'", ErrClientNotFound, err)
	}

	router.RemoveClient("node1")
	_, err = router.GetClient("node1")
	if err != ErrClientNotFound {
		t.Errorf("Expected error '%s', but got '%s'", ErrClientNotFound, err)
	}

	router.RemoveClient("missing") // removing a non-existing key doesn't panic
}
