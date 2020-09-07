package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/urfave/cli/v2"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	_ "github.com/lib/pq"
)

func main() {
	app := &cli.App{
		Name:  "todod",
		Usage: "Example todo service",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "server-addr",
				Usage:   "server listen address",
				Value:   ":8080",
				EnvVars: []string{"TODO_SERVER_ADDR"},
			},
			&cli.DurationFlag{
				Name:    "server-shutdown-timeout",
				Usage:   "server graceful shutdown timeout",
				Value:   20 * time.Second,
				EnvVars: []string{"TODO_SERVER_SHUTDOWN_TIMEOUT"},
			},
			&cli.StringFlag{
				Name:    "dsn",
				Usage:   "database connect string",
				Value:   "host=127.0.0.1 user=postgres password=s3cret dbname=todo sslmode=disable",
				EnvVars: []string{"TODO_DSN"},
			},
			&cli.IntFlag{
				Name:    "max-open-conns",
				Usage:   "maximum number of open connections to the database",
				Value:   10,
				EnvVars: []string{"TODO_MAX_OPEN_CONNS"},
			},
			&cli.IntFlag{
				Name:    "max-idle-conns",
				Usage:   "maximum number of connections in the idle connection pool",
				Value:   10,
				EnvVars: []string{"TODO_MAX_IDLE_CONNS"},
			},
			&cli.DurationFlag{
				Name:    "conn-max-lifetime",
				Usage:   "maximum amount of time a connection may be reused.",
				Value:   9 * time.Minute,
				EnvVars: []string{"TODO_CONN_MAX_LIFETIME"},
			},
		},
		Action: run,
	}
	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func run(c *cli.Context) error {
	db, err := newDB(c)
	if err != nil {
		return err
	}
	defer func() {
		log.Print("db pool closing")
		if err := db.Close(); err != nil {
			log.Printf("could not close db pool: %v", err)
		} else {
			log.Print("db pool closed")
		}
	}()

	api, err := newAPI(c, db)
	if err != nil {
		return err
	}

	srv, err := newServer(c, api.Handler())
	if err != nil {
		return err
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		if err := srv.Close(); err != nil {
			log.Printf("could not shutdown api server: %v", err)
		}
	}()

	return srv.Start()
}

func newServer(c *cli.Context, handler http.Handler) (*Server, error) {
	mux := chi.NewMux()
	mux.Use(middleware.DefaultLogger)
	mux.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "OK")
	})
	mux.Mount("/todo", handler)

	return &Server{
		server: &http.Server{
			Addr:    c.String("server-addr"),
			Handler: mux,
		},
		shutdownTimeout: c.Duration("server-shutdown-timeout"),
	}, nil
}

type Server struct {
	server          *http.Server
	shutdownTimeout time.Duration
}

func (s *Server) Close() error {
	log.Println("api server shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("could not shutdown api server: %v", err)
	}
	return nil
}

func (s *Server) Start() error {
	log.Println("api server started")
	if err := s.server.ListenAndServe(); err != nil {
		if err != http.ErrServerClosed {
			return fmt.Errorf("could not start api server: %v", err)
		}
	}
	log.Println("api server stopped")
	return nil
}

func newAPI(c *cli.Context, db *sql.DB) (*TodoAPI, error) {
	return &TodoAPI{
		store: &TodoStore{
			db: db,
		},
		respnd: jsonResponder{},
	}, nil
}

type Todo struct {
	ID          int    `json:"id" db:"id"`
	Name        string `json:"name" db:"name"`
	Description string `json:"description" db:"description"`
}

type TodoAPI struct {
	store  *TodoStore
	respnd jsonResponder
}

func (a *TodoAPI) Handler() http.Handler {
	h := chi.NewMux()
	h.Get("/", a.List)
	h.Get("/{id}", a.Get)
	h.Post("/", a.Create)
	h.Put("/{id}", a.Update)
	h.Delete("/{id}", a.Delete)
	return h
}

func (a *TodoAPI) List(w http.ResponseWriter, r *http.Request) {
	todos, err := a.store.List(r.Context())
	if err != nil {
		a.respnd.Error(w, http.StatusInternalServerError, err)
		return
	}

	a.respnd.Success(w, http.StatusOK, todos)
}

func (a *TodoAPI) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		a.respnd.Error(w, http.StatusBadRequest, err)
		return
	}

	todo, err := a.store.Get(r.Context(), id)
	if err != nil {
		a.respnd.Error(w, http.StatusInternalServerError, err)
		return
	}
	if todo == nil {
		a.respnd.Error(w, http.StatusNotFound, fmt.Errorf("todo %d not found", id))
		return
	}

	a.respnd.Success(w, http.StatusOK, todo)
}

func (a *TodoAPI) Create(w http.ResponseWriter, r *http.Request) {
	var todo Todo
	err := json.NewDecoder(r.Body).Decode(&todo)
	if err != nil {
		a.respnd.Error(w, http.StatusBadRequest, err)
		return
	}

	err = a.store.Create(r.Context(), &todo)
	if err != nil {
		a.respnd.Error(w, http.StatusInternalServerError, err)
		return
	}

	a.respnd.Success(w, http.StatusCreated, todo)
}

func (a *TodoAPI) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		a.respnd.Error(w, http.StatusBadRequest, err)
		return
	}

	var todo Todo
	err = json.NewDecoder(r.Body).Decode(&todo)
	if err != nil {
		a.respnd.Error(w, http.StatusBadRequest, err)
		return
	}

	if todo.ID != 0 && todo.ID != id {
		a.respnd.Error(w, http.StatusBadRequest, fmt.Errorf("id does not match"))
		return
	}

	err = a.store.Update(r.Context(), &todo)
	if err != nil {
		a.respnd.Error(w, http.StatusInternalServerError, err)
		return
	}

	a.respnd.Success(w, http.StatusOK, todo)
}

func (a *TodoAPI) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		a.respnd.Error(w, http.StatusBadRequest, err)
		return
	}

	err = a.store.Delete(r.Context(), id)
	if err != nil {
		a.respnd.Error(w, http.StatusBadRequest, err)
		return
	}

	a.respnd.Success(w, http.StatusOK, nil)
}

type Response struct {
	Data  interface{} `json:"data,omitempty"`
	Error *Error      `json:"error,omitempty"`
}

type Error struct {
	Msg string `json:"msg"`
}

type jsonResponder struct{}

func (r jsonResponder) Error(w http.ResponseWriter, statusCode int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(Response{Error: &Error{Msg: err.Error()}})
}

func (r jsonResponder) Success(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(Response{Data: data})
}

type TodoStore struct {
	db *sql.DB
}

func (s *TodoStore) List(ctx context.Context) ([]*Todo, error) {
	query := "SELECT id, name, description FROM todos"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var todos []*Todo
	for rows.Next() {
		var todo Todo
		if err := rows.Scan(&todo.ID, &todo.Name, &todo.Description); err != nil {
			return nil, err
		}
		todos = append(todos, &todo)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return todos, nil
}

func (s *TodoStore) Get(ctx context.Context, id int) (*Todo, error) {
	var todo Todo
	query := "SELECT id, name, description FROM todos WHERE id = $1"
	row := s.db.QueryRowContext(ctx, query, id)
	err := row.Scan(&todo.ID, &todo.Name, &todo.Description)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &todo, nil
}

func (s *TodoStore) Create(ctx context.Context, todo *Todo) error {
	if todo == nil {
		return nil
	}
	var id int
	query := "INSERT INTO todos (name, description) VALUES ($1, $2) RETURNING id"
	row := s.db.QueryRowContext(ctx, query, todo.Name, todo.Description)
	err := row.Scan(&id)
	if err != nil {
		return err
	}
	todo.ID = id
	return nil
}

func (s *TodoStore) Update(ctx context.Context, todo *Todo) error {
	if todo == nil {
		return nil
	}
	query := "UPDATE todos SET name = $1, description = $2 WHERE id = $3 RETURNING name, desc"
	row := s.db.QueryRowContext(ctx, query, todo.Name, todo.Description, todo.ID)
	err := row.Scan(&todo.Name, &todo.Description)
	if err != nil {
		return err
	}
	return nil
}

func (s *TodoStore) Delete(ctx context.Context, id int) error {
	query := "DELETE FROM todos WHERE id = $3"
	_, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	return nil
}

func newDB(c *cli.Context) (*sql.DB, error) {
	db, err := sql.Open("postgres", c.String("dsn"))
	if err != nil {
		return nil, fmt.Errorf("could not open db: %v", err)
	}
	db.SetMaxOpenConns(c.Int("max-open-conns"))
	db.SetMaxIdleConns(c.Int("max-idle-conns"))
	db.SetConnMaxLifetime(c.Duration("conn-max-lifetime"))

	if err := func() error {
		var err error
		for _, n := range []int{50, 100, 200, 600, 1200} {
			if err = func() error {
				ctx, cancel := context.WithTimeout(context.Background(),
					3*time.Second)
				defer cancel()
				return db.PingContext(ctx)
			}(); err != nil {
				time.Sleep(time.Duration(n) * time.Millisecond)
				continue
			}
			break
		}
		return err
	}(); err != nil {
		return nil, fmt.Errorf("could not ping db: %v", err)
	}
	return db, nil
}
